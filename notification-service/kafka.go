package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/segmentio/kafka-go"
)

// Event simulating the order event payload
type OrderEvent struct {
	ID     string  `json:"id"`
	UserID string  `json:"user_id"`
	FundID string  `json:"fund_id"`
	Amount float64 `json:"amount"`
	Status string  `json:"status"`
}

func startKafkaConsumer() {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		brokers = "localhost:29092"
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers},
		Topic:   "order_events",
		GroupID: "notification-group", // Consumer group
	})

	defer reader.Close()

	log.Printf("Connected to Kafka Brokers at %s. Listening to topic 'order_events'...", brokers)

	for {
		m, err := reader.ReadMessage(context.Background())
		if err != nil {
			log.Printf("Error reading message from kafka: %v", err)
			continue
		}

		var event OrderEvent
		if err := json.Unmarshal(m.Value, &event); err != nil {
			log.Printf("Failed to unmarshal order event: %v", err)
			continue
		}

		sendAlerts(event)
	}
}

func sendAlerts(event OrderEvent) {
	// Simulate sending email and SMS based on status
	log.Println("--------------------------------------------------")
	log.Printf("🚨 [ALERT TRIGGERED] Order %s status changed to %s", event.ID, event.Status)
	
	emailBody := fmt.Sprintf("Dear %s,\nYour order for %s (Amount: %.2f) is now %s.\nThank you for using FundKit.", event.UserID, event.FundID, event.Amount, event.Status)
	smsBody := fmt.Sprintf("FundKit Alert: Order %s is %s.", event.ID, event.Status)

	log.Printf("📧 [EMAIL SENT] To: %s@example.com\nBody:\n%s", event.UserID, emailBody)
	log.Printf("📱 [SMS SENT] To: User %s | Message: %s", event.UserID, smsBody)
	log.Println("--------------------------------------------------")
}
