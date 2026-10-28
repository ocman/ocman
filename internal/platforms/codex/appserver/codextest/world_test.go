package codextest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

type turns chan string

func (t turns) Notification(method string, params json.RawMessage) {
	if method == codexappserver.NotifyAgentMessageDelta || method == codexappserver.NotifyTurnCompleted {
		t <- method
	}
}
func (turns) Request(json.RawMessage, string, json.RawMessage) {}

func TestWorld(t *testing.T) {
	s, pipes := New(t)
	World(s)
	events := make(turns, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := codexappserver.Attach(ctx, pipes.Reader, pipes.Writer, codexappserver.Options{}, events)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	threads, err := c.ListThreads(ctx, codexappserver.ThreadListParams{})
	if err != nil || len(threads) != 1 || threads[0].ID != StoredThread || threads[0].Cwd != Directory {
		t.Fatalf("threads = %+v %v", threads, err)
	}
	archived := true
	if threads, _ := c.ListThreads(ctx, codexappserver.ThreadListParams{Archived: &archived}); len(threads) != 0 {
		t.Fatalf("archived = %+v", threads)
	}
	items, err := c.ListItems(ctx, StoredThread)
	if err != nil || len(items) != 2 || items[1].Item.Text != StoredReply {
		t.Fatalf("items = %+v %v", items, err)
	}
	if turns, _ := c.ListTurns(ctx, StoredThread, "notLoaded"); len(turns) != 1 || turns[0].Status != "completed" {
		t.Fatalf("turns = %+v", turns)
	}
	start, err := c.StartThread(ctx, codexappserver.ThreadStartParams{Cwd: "/elsewhere"})
	if err != nil || start.Thread.ID == StoredThread || start.Thread.Cwd != "/elsewhere" {
		t.Fatalf("start = %+v %v", start, err)
	}
	if empty, _ := c.ListItems(ctx, start.Thread.ID); len(empty) != 0 {
		t.Fatalf("new thread items = %+v", empty)
	}
	if empty, _ := c.ListTurns(ctx, start.Thread.ID, "notLoaded"); len(empty) != 0 {
		t.Fatalf("new thread turns = %+v", empty)
	}
	if th, err := c.ReadThread(ctx, start.Thread.ID, false); err != nil || th.ID != start.Thread.ID {
		t.Fatalf("read = %+v %v", th, err)
	}
	if _, err := c.ResumeThread(ctx, codexappserver.ThreadResumeParams{ThreadID: StoredThread}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetThreadName(ctx, StoredThread, "n"); err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(ctx, codexappserver.TurnStartParams{ThreadID: start.Thread.ID, Input: []codexappserver.UserInput{codexappserver.TextInput("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.InterruptTurn(ctx, start.Thread.ID, turn.ID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{codexappserver.NotifyAgentMessageDelta, codexappserver.NotifyTurnCompleted} {
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("event = %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("no %s", want)
		}
	}
}
