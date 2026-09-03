// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
	"github.com/dhanush-cn/fundkit/order-service/internal/service"
)

func init() { gin.SetMode(gin.TestMode) }

// The handler tests run against the real service with in-memory adapters, so
// they cover the transport contract and the use case wiring together.
type memoryRepo struct {
	mu     sync.Mutex
	orders map[string]*domain.Order
	outbox []domain.OutboxMessage
	seq    int
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{orders: make(map[string]*domain.Order)}
}

func (m *memoryRepo) CreateWithOutbox(_ context.Context, order *domain.Order, build domain.OutboxBuilder) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	order.ID = "order-" + string(rune('a'+m.seq-1))
	order.CreatedAt = time.Now()
	order.UpdatedAt = order.CreatedAt

	msg, err := build(*order)
	if err != nil {
		return err
	}

	stored := *order
	m.orders[order.ID] = &stored
	m.outbox = append(m.outbox, msg)
	return nil
}

func (m *memoryRepo) GetByID(_ context.Context, id string) (*domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	copied := *order
	return &copied, nil
}

func (m *memoryRepo) List(_ context.Context, _ int) ([]domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]domain.Order, 0, len(m.orders))
	for _, order := range m.orders {
		list = append(list, *order)
	}
	return list, nil
}

func (m *memoryRepo) UpdateStatusWithOutbox(_ context.Context, id string, expected, next domain.OrderStatus, build domain.OutboxBuilder) (*domain.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	if order.Status != expected {
		return nil, domain.ErrInvalidTransition
	}

	candidate := *order
	candidate.Status = next
	candidate.UpdatedAt = time.Now()

	msg, err := build(candidate)
	if err != nil {
		return nil, err
	}

	*order = candidate
	m.outbox = append(m.outbox, msg)

	copied := candidate
	return &copied, nil
}

func (m *memoryRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.orders[id]; !ok {
		return domain.ErrOrderNotFound
	}
	delete(m.orders, id)
	return nil
}

type memoryIdem struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newMemoryIdem() *memoryIdem { return &memoryIdem{seen: make(map[string]bool)} }

func (m *memoryIdem) ClaimIdempotency(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.seen[key] {
		return false, nil
	}
	m.seen[key] = true
	return true, nil
}

func (m *memoryIdem) ConfirmIdempotency(context.Context, string) error { return nil }

func (m *memoryIdem) ReleaseIdempotency(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, key)
	return nil
}

func newOrderTestRouter() (*gin.Engine, *service.OrderService) {
	// No publisher: the service records events into the repository's outbox
	// inside the same call, and the relay is what would put them on Kafka.
	orders := service.NewOrderService(
		newMemoryRepo(),
		newMemoryIdem(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	router := gin.New()
	handler := NewOrderHandler(orders)
	router.POST("/orders", handler.Create)
	router.GET("/orders", handler.List)
	router.GET("/orders/:id", handler.Get)
	router.PATCH("/orders/:id", handler.UpdateStatus)
	router.DELETE("/orders/:id", handler.Delete)

	return router, orders
}

func orderBody() map[string]any {
	return map[string]any{
		"fund_id": "quant-small-cap-fund",
		// Paise, not rupees: the API takes an integer and rejects a decimal.
		"amount":          500000,
		"type":            "SIP",
		"idempotency_key": "key-1",
	}
}

func postOrder(t *testing.T, router *gin.Engine, body map[string]any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

// The gateway's verified identity is the authority on who placed an order. A
// user_id in the body is only a fallback for direct, gateway-less calls.
func TestCreateTakesIdentityFromTheGatewayHeaders(t *testing.T) {
	t.Parallel()

	router, orders := newOrderTestRouter()
	body := orderBody()
	body["user_id"] = "somebody-else"

	recorder := postOrder(t, router, body, map[string]string{
		HeaderUserID:    "user-verified",
		HeaderUsername:  "dhanush",
		HeaderUserEmail: "dhanush@example.com",
		HeaderUserPhone: "+919876543210",
		HeaderUserName:  "Dhanush C N",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	orders.Drain()

	var created domain.Order
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if created.UserID != "user-verified" {
		t.Fatalf("user id = %q; the body must not be able to override the verified subject", created.UserID)
	}
	if created.UserEmail != "dhanush@example.com" || created.UserPhone != "+919876543210" {
		t.Fatalf("contact details missing: %+v", created)
	}
	if created.UserName != "Dhanush C N" {
		t.Fatalf("display name = %q", created.UserName)
	}
}

func TestCreateFallsBackToTheBodyWhenCalledDirectly(t *testing.T) {
	t.Parallel()

	router, orders := newOrderTestRouter()
	body := orderBody()
	body["user_id"] = "user-direct"

	recorder := postOrder(t, router, body, nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	orders.Drain()
}

func TestCreateRequiresAUserFromSomewhere(t *testing.T) {
	t.Parallel()

	router, _ := newOrderTestRouter()

	recorder := postOrder(t, router, orderBody(), nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when nothing identifies the customer", recorder.Code)
	}
}

func TestCreateValidatesThePayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing fund", mutate: func(body map[string]any) { delete(body, "fund_id") }},
		{name: "zero amount", mutate: func(body map[string]any) { body["amount"] = 0 }},
		{name: "negative amount", mutate: func(body map[string]any) { body["amount"] = -100 }},
		// A client still thinking in rupees sends 100.50. That must be a loud
		// 400 rather than a quiet truncation to ₹1.00 — encoding/json refuses
		// to decode a fractional number into the integer paise field, which is
		// the whole reason the wire type is an integer.
		{name: "decimal amount", mutate: func(body map[string]any) { body["amount"] = 100.50 }},
		{name: "unknown order type", mutate: func(body map[string]any) { body["type"] = "SWING-TRADE" }},
		{name: "missing idempotency key", mutate: func(body map[string]any) { delete(body, "idempotency_key") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			router, _ := newOrderTestRouter()
			body := orderBody()
			test.mutate(body)

			recorder := postOrder(t, router, body, map[string]string{HeaderUserID: "user-1"})
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreateReportsAReplayAsAConflict(t *testing.T) {
	t.Parallel()

	router, orders := newOrderTestRouter()
	headers := map[string]string{HeaderUserID: "user-1"}

	if recorder := postOrder(t, router, orderBody(), headers); recorder.Code != http.StatusCreated {
		t.Fatalf("first order: %d", recorder.Code)
	}
	orders.Drain()

	recorder := postOrder(t, router, orderBody(), headers)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a replayed idempotency key", recorder.Code)
	}
}

func TestGetUnknownOrderIs404(t *testing.T) {
	t.Parallel()

	router, _ := newOrderTestRouter()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/orders/nope", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestUpdateStatusRejectsAnUnknownStatus(t *testing.T) {
	t.Parallel()

	router, _ := newOrderTestRouter()
	payload, err := json.Marshal(map[string]string{"status": "ALMOST_DONE"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/orders/order-a", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
