// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"fmt"
	"sync"
	"testing"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestAddressBookGivesEachAddressToOneClaimant(t *testing.T) {
	t.Parallel()

	book := &addressBook{store: state.NewFileStore(t.TempDir())}
	var wg sync.WaitGroup
	wins := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			if ok, _, err := book.claim("sn-1", "10.0.1.10", owner); err == nil && ok {
				wins <- owner
			}
		}(fmt.Sprintf("compute-%d", i))
	}
	wg.Wait()
	close(wins)
	if n := len(wins); n != 1 {
		t.Fatalf("%d claimants won 10.0.1.10, want exactly one", n)
	}
}

func TestAddressBookAllocatesDistinctAddressesAtOnce(t *testing.T) {
	t.Parallel()

	book := &addressBook{store: state.NewFileStore(t.TempDir())}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got = map[string]string{}
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			// Every agent starts from the same stale view: nothing taken.
			ip, err := book.allocate("10.0.1.0/24", "sn-1", owner, map[string]bool{})
			if err != nil {
				t.Errorf("%s: %v", owner, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if other, dup := got[ip]; dup {
				t.Errorf("%s and %s both got %s", owner, other, ip)
			}
			got[ip] = owner
		}(fmt.Sprintf("compute-%d", i))
	}
	wg.Wait()
	if len(got) != 8 {
		t.Fatalf("got %d distinct addresses, want 8", len(got))
	}
}

func TestAddressBookReleasesOnlyItsOwnReservation(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	book := &addressBook{store: store}
	if ok, _, _ := book.claim("sn-1", "10.0.1.10", "a"); !ok {
		t.Fatal("claim")
	}
	if err := book.release("sn-1", "10.0.1.10", "b"); err != nil {
		t.Fatal(err)
	}
	if ok, owner, _ := book.claim("sn-1", "10.0.1.10", "c"); ok || owner != "a" {
		t.Fatalf("another owner's release freed a's address: ok=%v owner=%s", ok, owner)
	}
	if err := book.release("sn-1", "10.0.1.10", "a"); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := book.claim("sn-1", "10.0.1.10", "c"); !ok {
		t.Fatal("the owner's release should free the address")
	}
}
