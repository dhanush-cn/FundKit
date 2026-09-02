package main

import (
	"fmt"
	"log"
	"time"

	"order-service/cache"
	"order-service/database"
	"order-service/messaging"
	"order-service/models"
)

const idempotencyTTL = 24 * time.Hour

type CreateOrderRequest struct {
	UserID         string           `json:"user_id" binding:"required"`
	FundID         string           `json:"fund_id" binding:"required"`
	Amount         float64          `json:"amount" binding:"required,gt=0"`
	Type           models.OrderType `json:"type" binding:"required,oneof=SIP LUMPSUM"`
	IdempotencyKey string           `json:"idempotency_key" binding:"required"`
}

type UpdateOrderStatusRequest struct {
	Status models.OrderStatus `json:"status" binding:"required,oneof=PENDING PROCESSING EXECUTED FAILED"`
}

func persistOrderStatus(orderID string, nextStatus models.OrderStatus) error {
	result := database.DB.Model(&models.Order{}).
		Where("id = ?", orderID).
		Update("status", nextStatus)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("order %s not found", orderID)
	}
	return nil
}

func publishOrderSnapshot(orderID string) {
	var order models.Order
	if err := database.DB.First(&order, "id = ?", orderID).Error; err != nil {
		log.Printf("unable to load order %s for event publication: %v", orderID, err)
		return
	}

	if err := messaging.PublishOrderEvent(order); err != nil {
		log.Printf("unable to publish order event for %s: %v", orderID, err)
	}
}

func processOrderLifecycle(orderID string) {
	var order models.Order
	if err := database.DB.First(&order, "id = ?", orderID).Error; err != nil {
		log.Printf("unable to start processing order %s: %v", orderID, err)
		return
	}

	if order.Status != models.StatusPending {
		return
	}

	if err := persistOrderStatus(order.ID, models.StatusProcessing); err != nil {
		log.Printf("unable to mark order %s as PROCESSING: %v", order.ID, err)
		return
	}
	publishOrderSnapshot(order.ID)

	finalStatus := models.StatusExecuted
	if order.Amount <= 0 {
		finalStatus = models.StatusFailed
	}

	if err := persistOrderStatus(order.ID, finalStatus); err != nil {
		log.Printf("unable to finalize order %s: %v", order.ID, err)
		return
	}
	publishOrderSnapshot(order.ID)

	cache.SetIdempotency(order.IdempotencyKey, idempotencyTTL)
}

func applyManualOrderStatus(order *models.Order, nextStatus models.OrderStatus) error {
	if !models.CanTransitionStatus(order.Status, nextStatus) {
		return fmt.Errorf("invalid transition from %s to %s", order.Status, nextStatus)
	}

	if err := persistOrderStatus(order.ID, nextStatus); err != nil {
		return err
	}

	order.Status = nextStatus
	publishOrderSnapshot(order.ID)
	return nil
}
