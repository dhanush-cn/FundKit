package messaging

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/segmentio/kafka-go"
)

var KafkaWriter *kafka.Writer

func InitKafka() {
	broker := os.Getenv("KAFKA_BROKERS")
	if broker == "" {
		broker = "localhost:9092"
	}

	parts := strings.Split(broker, ",")
	brokers := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			brokers = append(brokers, part)
		}
	}
	if len(brokers) == 0 {
		brokers = []string{"localhost:9092"}
	}

	KafkaWriter = &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    "order_events",
		Balancer: &kafka.LeastBytes{},
	}
}

func PublishOrderEvent(event interface{}) error {
	if KafkaWriter == nil {
		return nil
	}

	msgBytes, err := json.Marshal(event)
	if err != nil {
		return err
	}

	err = KafkaWriter.WriteMessages(context.Background(),
		kafka.Message{
			Value: msgBytes,
		},
	)
	if err != nil {
		log.Printf("Could not write message to Kafka: %v", err)
		return err
	}

	return nil
}
