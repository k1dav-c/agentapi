// Package discordbridge connects a running AgentAPI session to Discord through Temporal.
package discordbridge

import (
	"errors"
	"time"

	"github.com/coder/agentapi/lib/handoff"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const ReplySignal = "human_response"
const workflowPrefix = "agentapi-discord-"

type WorkflowInput struct {
	Request   handoff.Request
	MessageID string
	Deadline  time.Time
}

type FeedbackInput struct {
	MessageID string
	Outcome   string
}

// HumanReplyWorkflow persists the wait independently of the browser and worker.
// All HTTP and Discord operations run in Activities, never in workflow code.
func HumanReplyWorkflow(ctx workflow.Context, input WorkflowInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    30 * time.Second,
		ScheduleToCloseTimeout: 5 * time.Minute,
		RetryPolicy:            &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: 30 * time.Second},
	})
	if input.Deadline.IsZero() {
		input.Deadline = workflow.Now(ctx).Add(7 * 24 * time.Hour)
	}
	deliveryCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    30 * time.Second,
		ScheduleToCloseTimeout: 7 * 24 * time.Hour,
		RetryPolicy:            &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: time.Minute},
	})
	if input.MessageID == "" {
		if err := workflow.ExecuteActivity(deliveryCtx, "DiscordNotify", input.Request).Get(ctx, &input.MessageID); err != nil {
			var applicationError *temporal.ApplicationError
			if errors.As(err, &applicationError) && applicationError.Type() == "Superseded" {
				return nil
			}
			return err
		}
	}
	signals := workflow.GetSignalChannel(ctx, ReplySignal)
	finish := func(outcome string) error {
		return workflow.ExecuteActivity(deliveryCtx, "DiscordFeedback", FeedbackInput{input.MessageID, outcome}).Get(ctx, nil)
	}
	// Continue-as-new bounds history while keeping the same Workflow ID, request,
	// Discord message and expiry. Drain buffered replies before continuing.
	for steps := 0; ; steps++ {
		if steps >= 100 && signals.Len() == 0 {
			return workflow.NewContinueAsNewError(ctx, HumanReplyWorkflow, input)
		}
		if !workflow.Now(ctx).Before(input.Deadline) {
			return finish("expired")
		}
		var reply handoff.Reply
		received := false
		timerCtx, cancel := workflow.WithCancel(ctx)
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(signals, func(ch workflow.ReceiveChannel, _ bool) {
			ch.Receive(ctx, &reply)
			received = true
		})
		selector.AddFuture(workflow.NewTimer(timerCtx, time.Minute), func(workflow.Future) {})
		selector.Select(ctx)
		cancel()
		if received {
			if reply.RequestID != input.Request.ID || reply.ID == "" {
				continue
			}
			var result handoff.Result
			if err := workflow.ExecuteActivity(deliveryCtx, "AgentReply", reply).Get(ctx, &result); err != nil {
				return err
			}
			if result.Outcome == "invalid" {
				if err := workflow.ExecuteActivity(deliveryCtx, "DiscordFeedback", FeedbackInput{input.MessageID, "invalid"}).Get(ctx, nil); err != nil {
					return err
				}
				continue
			}
			return finish(result.Outcome)
		}
		var active bool
		if err := workflow.ExecuteActivity(ctx, "AgentPending", input.Request.ID).Get(ctx, &active); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			workflow.GetLogger(ctx).Warn("AgentAPI unavailable; keeping human reply pending")
			continue
		}
		if !active {
			return finish("superseded")
		}
	}
}
