package pingtunnel

import "testing"

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient("127.0.0.1:0", "127.0.0.1", "127.0.0.1:22", 60, 0, "0.0.0.0",
		1, 0, 0, 0, 0, 0, 0, 0, nil, nil, "", "")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	return c
}

func TestNewClientUniqueIDs(t *testing.T) {
	seen := make(map[int]bool)
	for i := 0; i < 200; i++ {
		c := newTestClient(t)
		if seen[c.id] {
			t.Fatalf("duplicate client id %d at iteration %d", c.id, i)
		}
		seen[c.id] = true
		if _, ok := usedClientIDs.Load(c.id); !ok {
			t.Fatalf("client id %d not registered in usedClientIDs", c.id)
		}
	}
}

func TestClientStopReleasesID(t *testing.T) {
	c := newTestClient(t)
	if _, ok := usedClientIDs.Load(c.id); !ok {
		t.Fatalf("client id %d not registered", c.id)
	}
	c.Stop()
	if _, ok := usedClientIDs.Load(c.id); ok {
		t.Fatalf("client id %d not released after Stop", c.id)
	}
}
