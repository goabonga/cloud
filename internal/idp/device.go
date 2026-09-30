// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// deviceGrantType is the RFC 8628 grant_type value for polling /token with a
// device_code.
const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// deviceCodeTTL is how long a device authorization stays pending before it
// expires. Ten minutes gives a human enough time to switch to a browser and
// type an eight-character code without leaving stale entries around long.
const deviceCodeTTL = 10 * time.Minute

// devicePollInterval is the minimum number of seconds a client is told to
// wait between polls. Not enforced server-side: an internal tool's own
// polling loop already respects the interval it was given.
const devicePollInterval = 5

const (
	deviceStatusPending  = "pending"
	deviceStatusApproved = "approved"
	deviceStatusDenied   = "denied"
)

// deviceAuth is one pending (or resolved) device authorization.
type deviceAuth struct {
	deviceCode string
	userCode   string
	status     string
	subject    string
	roles      []string
	expiresAt  time.Time
}

// deviceStore holds device authorizations in memory only: a code that never
// gets approved is dead within deviceCodeTTL either way, so nothing here
// needs to survive a restart. Expiry is checked lazily on access rather than
// swept in the background, since the expected volume never justifies a timer
// goroutine.
type deviceStore struct {
	mu       sync.Mutex
	byUser   map[string]*deviceAuth
	byDevice map[string]*deviceAuth
	now      func() time.Time
	ttl      time.Duration
}

func newDeviceStore(ttl time.Duration) *deviceStore {
	return &deviceStore{
		byUser:   make(map[string]*deviceAuth),
		byDevice: make(map[string]*deviceAuth),
		now:      time.Now,
		ttl:      ttl,
	}
}

// create starts a new pending device authorization.
func (d *deviceStore) create() (*deviceAuth, error) {
	userCode, err := generateUserCode()
	if err != nil {
		return nil, err
	}
	deviceCode, err := generateDeviceCode()
	if err != nil {
		return nil, err
	}
	da := &deviceAuth{
		deviceCode: deviceCode,
		userCode:   userCode,
		status:     deviceStatusPending,
		expiresAt:  d.now().Add(d.ttl),
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.byDevice[deviceCode] = da
	d.byUser[userCode] = da
	return da, nil
}

// byDeviceCode returns a snapshot of the authorization for deviceCode, or
// false if it is absent or expired (an expired entry is dropped).
func (d *deviceStore) byDeviceCode(code string) (deviceAuth, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	da, ok := d.byDevice[code]
	if !ok {
		return deviceAuth{}, false
	}
	if d.now().After(da.expiresAt) {
		d.removeLocked(da)
		return deviceAuth{}, false
	}
	return *da, true
}

// approve marks the pending authorization for userCode as approved by
// subject, carrying roles. It reports whether a pending, unexpired
// authorization was found.
func (d *deviceStore) approve(userCode, subject string, roles []string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	da, ok := d.byUser[userCode]
	if !ok || d.now().After(da.expiresAt) || da.status != deviceStatusPending {
		return false
	}
	da.status = deviceStatusApproved
	da.subject = subject
	da.roles = roles
	return true
}

// deny marks the pending authorization for userCode as denied. It reports
// whether a pending, unexpired authorization was found.
func (d *deviceStore) deny(userCode string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	da, ok := d.byUser[userCode]
	if !ok || d.now().After(da.expiresAt) || da.status != deviceStatusPending {
		return false
	}
	da.status = deviceStatusDenied
	return true
}

// delete removes the authorization for deviceCode, terminal outcomes are
// single-use: once /token has returned a token or an access_denied error for
// a code, polling again must not repeat it.
func (d *deviceStore) delete(deviceCode string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if da, ok := d.byDevice[deviceCode]; ok {
		d.removeLocked(da)
	}
}

// removeLocked removes da from both indexes. Callers must hold d.mu.
func (d *deviceStore) removeLocked(da *deviceAuth) {
	delete(d.byDevice, da.deviceCode)
	delete(d.byUser, da.userCode)
}

// deviceCodeAlphabet avoids characters that are easily confused when a human
// copies an 8-character code by hand (0/O, 1/I/L). Its length is a power of
// two so a byte modulo it introduces no bias.
const deviceCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generateUserCode returns an 8-character, human-typeable code formatted
// XXXX-XXXX, matching RFC 8628's own example shape.
func generateUserCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i, c := range b {
		b[i] = deviceCodeAlphabet[int(c)%len(deviceCodeAlphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}

// generateDeviceCode returns a high-entropy opaque code for the polling
// client; unlike the user code, nobody has to type it.
func generateDeviceCode() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
