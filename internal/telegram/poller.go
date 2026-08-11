package telegram

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/local/dev-cockpit/internal/assistant"
)

const (
	// minBackoff and maxBackoff bound the wait after a failed poll. Telegram
	// answers a second getUpdates for the same bot with 409, which happens on
	// every restart while the old process still hangs in its long poll, so the
	// low end has to stay small enough to catch up quickly.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// nextBackoff doubles up to the ceiling.
func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}

// sleepCtx waits unless the channel is shutting down. It reports whether the
// wait ran out on its own.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// run is the poller. It reads the offset from the file on every round, so a
// token change or an external reset takes effect without restarting the loop,
// and it writes the offset back after a message was delivered.
func (c *Channel) run(ctx context.Context, cl *client, done chan struct{}) {
	defer c.finish(done)

	c.startupNotice(ctx, cl)

	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		offset := c.store.Load().Offset
		updates, err := cl.getUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// A rejected token does not start working by itself, so the poller
			// stops and says so on the settings page instead of asking Telegram
			// about the same wrong token every thirty seconds. Everything else,
			// 409 included, is weather: the old process may still hold the one
			// long poll this bot is allowed.
			if apiCode(err) == http.StatusUnauthorized {
				log.Printf("telegram: the poller stopped, %v", err)
				c.halt("Telegram rejected the bot token.")
				return
			}
			log.Printf("telegram: poll failed, retrying in %s: %v", backoff, err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = minBackoff

		stale := 0
		for _, update := range updates {
			if c.isStale(update) {
				stale++
			} else {
				c.handle(ctx, cl, update)
			}
			// After delivery, never before: a crash in the middle repeats the
			// message instead of losing it.
			c.advance(update.UpdateID)
			if ctx.Err() != nil {
				return
			}
		}
		if stale > 0 {
			log.Printf("telegram: dropped %d message(s) older than %s", stale, staleAfter)
		}
	}
}

// finish releases the poller slot. The status is left as it stands, so a poller
// that stopped on a rejected token keeps saying so.
func (c *Channel) finish(done chan struct{}) {
	c.mu.Lock()
	if c.done == done {
		c.cancel, c.done = nil, nil
	}
	c.mu.Unlock()
	close(done)
}

func (c *Channel) halt(reason string) {
	c.mu.Lock()
	c.status = Status{State: StateStopped, Reason: reason}
	c.mu.Unlock()
}

func (c *Channel) advance(updateID int64) {
	c.store.Update(func(s *State) {
		if updateID >= s.Offset {
			s.Offset = updateID + 1
		}
	})
}

// isStale reports whether a message waited out a server pause. After two days
// off nobody wants the assistant working through the messages of the day
// before yesterday, so they are counted and dropped.
func (c *Channel) isStale(update Update) bool {
	if update.Message == nil || update.Message.Date == 0 {
		return false
	}
	return c.now().Sub(time.Unix(update.Message.Date, 0)) > staleAfter
}

// startupNotice closes the one gap a restart leaves: the process that would
// have sent the answer is gone, and with it the hook. If the live conversation
// ends in a question without an answer, or in an answer that broke off, and it
// is recent, the chat hears about it once. The message id is written down, so
// the next start says nothing about the same turn.
func (c *Channel) startupNotice(ctx context.Context, cl *client) {
	st := c.store.Load()
	if st.ChatID == 0 {
		return
	}
	conversation, ok := c.conversations.Current()
	if !ok {
		return
	}
	last, ok := conversation.Last()
	if !ok || !tornOff(last) || st.LastNoticeMessageID == last.ID {
		return
	}
	if c.now().Sub(last.CreatedAt) > noticeMaxAge {
		return
	}
	// A turn this process adopted from its predecessor is still running and
	// will report through the hook like any other.
	if c.conversations.Running(conversation.ID) {
		return
	}
	if err := c.send(ctx, cl, st.ChatID, "The cockpit restarted while the last turn was still running, so that answer never arrived. Send the message again."); err != nil {
		return
	}
	c.store.Update(func(s *State) { s.LastNoticeMessageID = last.ID })
}

// tornOff reports whether the last message of a conversation is one nobody is
// going to answer any more.
func tornOff(last assistant.Message) bool {
	if last.Role == assistant.RoleUser {
		return true
	}
	return last.State != assistant.StateComplete
}
