package storage

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

const AgentSessionEventNotifyChannel = "agent_session_events"

type AgentSessionEventNotification struct {
	SessionID string `json:"session_id"`
	Seq       uint64 `json:"seq"`
}

type AgentSessionEventListener interface {
	ListenAgentSessionEventNotifications(ctx context.Context, handler func(context.Context, AgentSessionEventNotification)) error
}

func (s *PostgresNativeStorage) ListenAgentSessionEventNotifications(ctx context.Context, handler func(context.Context, AgentSessionEventNotification)) error {
	if s == nil || s.pool == nil {
		return nil
	}
	if handler == nil {
		return ErrInvalidInput
	}
	for {
		if err := s.listenAgentSessionEventNotificationsOnce(ctx, handler); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("component=agent_session phase=pg_notify_listen error=%v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *PostgresNativeStorage) listenAgentSessionEventNotificationsOnce(ctx context.Context, handler func(context.Context, AgentSessionEventNotification)) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{AgentSessionEventNotifyChannel}.Sanitize()); err != nil {
		return err
	}
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if notification == nil || notification.Channel != AgentSessionEventNotifyChannel {
			continue
		}
		var payload AgentSessionEventNotification
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			log.Printf("component=agent_session phase=pg_notify_decode payload=%q error=%v", notification.Payload, err)
			continue
		}
		if payload.SessionID == "" || payload.Seq == 0 {
			continue
		}
		handler(ctx, payload)
	}
}
