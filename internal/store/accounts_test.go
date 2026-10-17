package store

import (
	"context"
	"testing"
	"time"
)

func TestUsersAndConnections(t *testing.T) {
	s, ctx := open(t), context.Background()
	id, err := s.UpsertUser(ctx, User{GitHubID: 7, Login: "tanay", Token: "sealed-1"})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.UpsertUser(ctx, User{GitHubID: 7, Login: "tanay-renamed", Token: "sealed-2"})
	if again != id {
		t.Fatalf("signing in again made a new user: %d vs %d", again, id)
	}
	u, _ := s.GetUser(ctx, id)
	if u.Login != "tanay-renamed" || u.Token != "sealed-2" {
		t.Fatalf("user not updated: %+v", u)
	}

	c := Connection{Name: "c-abc", UserID: id, Source: "github", Label: "tanay/holdfast", Secret: "s", TestApp: true,
		Repo: "tanay/holdfast", HookID: 99}
	if err := s.AddConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Connection(ctx, "c-abc")
	if err != nil || got.Label != "tanay/holdfast" || !got.TestApp || got.HookID != 99 {
		t.Fatalf("Connection = %+v %v", got, err)
	}
	if list, _ := s.Connections(ctx, id); len(list) != 1 {
		t.Fatalf("Connections = %v", list)
	}
	if list, _ := s.Connections(ctx, id+1); len(list) != 0 {
		t.Fatal("another user's connections leaked")
	}
	s.SetConnectionSecret(ctx, "c-abc", "s2")
	if got, _ := s.Connection(ctx, "c-abc"); got.Secret != "s2" {
		t.Fatal("secret not updated")
	}

	e := event("c-abc", time.Now())
	s.Insert(ctx, e)
	if err := s.DeleteConnection(ctx, "c-abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Connection(ctx, "c-abc"); err != ErrNotFound {
		t.Fatal("connection not deleted")
	}
	if _, err := s.Get(ctx, e.ID); err != ErrNotFound {
		t.Fatal("a deleted connection's events must go too")
	}
}
