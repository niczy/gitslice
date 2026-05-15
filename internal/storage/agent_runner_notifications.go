package storage

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const AgentRunnerNotifyChannel = "agent_runner_updates"

type AgentRunnerNotification struct {
	UserID   string `json:"user_id"`
	RunnerID string `json:"runner_id"`
}

type AgentRunnerNotifier interface {
	NotifyAgentRunnerUpdate(ctx context.Context, userID, runnerID string) error
}

type AgentRunnerListener interface {
	ListenAgentRunnerNotifications(ctx context.Context, handler func(context.Context, AgentRunnerNotification)) error
}

func (s *PostgresNativeStorage) NotifyAgentRunnerUpdate(ctx context.Context, userID, runnerID string) error {
	ctx = ensureCtx(ctx)
	userID = strings.TrimSpace(userID)
	runnerID = strings.TrimSpace(runnerID)
	if userID == "" {
		return ErrInvalidInput
	}
	_, err := s.pool.Exec(ctx, `
		SELECT pg_notify($1, json_build_object('user_id', $2::text, 'runner_id', $3::text)::text)
	`, AgentRunnerNotifyChannel, userID, runnerID)
	return err
}

func (s *PostgresNativeStorage) ListenAgentRunnerNotifications(ctx context.Context, handler func(context.Context, AgentRunnerNotification)) error {
	if s == nil || s.pool == nil {
		return nil
	}
	if handler == nil {
		return ErrInvalidInput
	}
	for {
		if err := s.listenAgentRunnerNotificationsOnce(ctx, handler); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("component=agent_runner phase=pg_notify_listen error=%v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *PostgresNativeStorage) listenAgentRunnerNotificationsOnce(ctx context.Context, handler func(context.Context, AgentRunnerNotification)) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{AgentRunnerNotifyChannel}.Sanitize()); err != nil {
		return err
	}
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if notification == nil || notification.Channel != AgentRunnerNotifyChannel {
			continue
		}
		var payload AgentRunnerNotification
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			log.Printf("component=agent_runner phase=pg_notify_decode payload=%q error=%v", notification.Payload, err)
			continue
		}
		if payload.UserID == "" {
			continue
		}
		handler(ctx, payload)
	}
}
