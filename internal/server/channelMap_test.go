package server

import (
	"net"
	"sync"
	"testing"
)

// fakeConn is a minimal net.Conn for use as a map key/value
type fakeConn struct{ net.Conn }

func newFakeConn() *fakeConn { return &fakeConn{} }

func TestChannelMap_AddAndGet(t *testing.T) {
	cm := NewChannelMap()
	c := newFakeConn()

	if err := cm.add("foo", c); err != nil {
		t.Fatalf("add returned unexpected error: %v", err)
	}

	got, err := cm.get("foo")
	if err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if got != c {
		t.Fatalf("get returned wrong conn: want %p got %p", c, got)
	}

	key, err := cm.getKey(c)
	if err != nil {
		t.Fatalf("getKey returned error: %v", err)
	}
	if key != "foo" {
		t.Fatalf("getKey returned %q, want %q", key, "foo")
	}
}

func TestChannelMap_AddDuplicateKey(t *testing.T) {
	cm := NewChannelMap()
	if err := cm.add("foo", newFakeConn()); err != nil {
		t.Fatalf("first add failed: %v", err)
	}
	if err := cm.add("foo", newFakeConn()); err == nil {
		t.Fatal("expected error on duplicate key, got nil")
	}
}

func TestChannelMap_AddDuplicateConn(t *testing.T) {
	cm := NewChannelMap()
	c := newFakeConn()
	if err := cm.add("foo", c); err != nil {
		t.Fatalf("first add failed: %v", err)
	}
	if err := cm.add("bar", c); err == nil {
		t.Fatal("expected error registering same conn under second key, got nil")
	}
}

func TestChannelMap_RemoveFreesBothMaps(t *testing.T) {
	cm := NewChannelMap()
	c := newFakeConn()
	if err := cm.add("foo", c); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	got, err := cm.rem("foo")
	if err != nil {
		t.Fatalf("rem returned error: %v", err)
	}
	if got != c {
		t.Fatalf("rem returned wrong conn")
	}

	if _, err := cm.get("foo"); err == nil {
		t.Error("get should fail after rem")
	}
	if _, err := cm.getKey(c); err == nil {
		t.Error("getKey should fail after rem (connKey not cleaned up)")
	}

	// After removal, the same key and conn must be reusable.
	if err := cm.add("foo", c); err != nil {
		t.Errorf("add after rem failed: %v", err)
	}
}

func TestChannelMap_RemoveMissingKey(t *testing.T) {
	cm := NewChannelMap()
	if _, err := cm.rem("nope"); err == nil {
		t.Error("expected error removing unknown key")
	}
}

func TestChannelMap_GetMissingKey(t *testing.T) {
	cm := NewChannelMap()
	if _, err := cm.get("nope"); err == nil {
		t.Error("expected error getting unknown key")
	}
}

func TestChannelMap_GetKeyMissingConn(t *testing.T) {
	cm := NewChannelMap()
	if _, err := cm.getKey(newFakeConn()); err == nil {
		t.Error("expected error getting key for unknown conn")
	}
}

// TestChannelMap_ConcurrentAccess exercises the mutex. Run with -race.
func TestChannelMap_ConcurrentAccess(t *testing.T) {
	cm := NewChannelMap()
	const workers = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			c := newFakeConn()
			key := string(rune('a'+id%26)) + string(rune('0'+id/26))
			// Best-effort: collisions are expected and acceptable here
			// the assertion is just that nothing panics or data-races
			_ = cm.add(key, c)
			_, _ = cm.get(key)
			_, _ = cm.getKey(c)
			_, _ = cm.rem(key)
		}(i)
	}
	wg.Wait()
}
