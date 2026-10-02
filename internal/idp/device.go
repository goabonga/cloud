// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package idp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
const deviceCodeTTL = 10 * time.Minute
const devicePollInterval = 5
const maxDeviceAuthorizations = 1024

var errDeviceCapacity = errors.New("too many pending device authorizations")

const (
	deviceStatusPending  = "pending"
	deviceStatusApproved = "approved"
	deviceStatusDenied   = "denied"
)

type deviceAuth struct {
	DeviceCode   string
	UserCode     string
	Status       string
	Subject      string
	Roles        []string
	ExpiresAt    time.Time
	LastPoll     time.Time
	PollInterval time.Duration
}

// deviceStore bounds pending authorizations and can share them across IdP instances.
type deviceStore struct {
	mu       sync.Mutex
	byUser   map[string]*deviceAuth
	byDevice map[string]*deviceAuth
	now      func() time.Time
	ttl      time.Duration
	backend  state.Store
}

func newDeviceStore(ttl time.Duration) *deviceStore {
	return &deviceStore{byUser: map[string]*deviceAuth{}, byDevice: map[string]*deviceAuth{}, now: time.Now, ttl: ttl}
}
func (d *deviceStore) transact(change func() error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for attempt := 0; attempt < 16; attempt++ {
		var old []byte
		if d.backend != nil {
			var err error
			old, err = d.backend.Get("idp/device-authorizations")
			if err != nil && !errors.Is(err, state.ErrNotFound) {
				return err
			}
			d.byDevice = map[string]*deviceAuth{}
			d.byUser = map[string]*deviceAuth{}
			if len(old) > 0 {
				if err := json.Unmarshal(old, &d.byDevice); err != nil {
					return err
				}
			}
			for _, da := range d.byDevice {
				d.byUser[da.UserCode] = da
			}
		}
		for _, da := range d.byDevice {
			if !da.ExpiresAt.After(d.now()) {
				d.removeLocked(da)
			}
		}
		if err := change(); err != nil {
			return err
		}
		if d.backend == nil {
			return nil
		}
		data, err := json.Marshal(d.byDevice)
		if err != nil {
			return err
		}
		ok, err := d.backend.CompareAndSwap("idp/device-authorizations", old, data)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("device authorizations: concurrent update limit exceeded")
}
func (d *deviceStore) create() (*deviceAuth, error) {
	userCode, err := generateUserCode()
	if err != nil {
		return nil, err
	}
	deviceCode, err := generateDeviceCode()
	if err != nil {
		return nil, err
	}
	da := &deviceAuth{DeviceCode: deviceCode, UserCode: userCode, Status: deviceStatusPending, ExpiresAt: d.now().Add(d.ttl)}
	err = d.transact(func() error {
		if len(d.byDevice) >= maxDeviceAuthorizations {
			return errDeviceCapacity
		}
		if _, exists := d.byUser[userCode]; exists {
			return fmt.Errorf("device user code collision; retry authorization")
		}
		d.byDevice[deviceCode] = da
		d.byUser[userCode] = da
		return nil
	})
	return da, err
}
func (d *deviceStore) byDeviceCode(code string) (deviceAuth, bool) {
	var out deviceAuth
	var ok bool
	err := d.transact(func() error {
		if da, found := d.byDevice[code]; found {
			out = *da
			ok = true
		}
		return nil
	})
	return out, ok && err == nil
}
func (d *deviceStore) approve(code, subject string, roles []string) bool {
	var approved bool
	err := d.transact(func() error {
		approved = false
		da, ok := d.byUser[code]
		if ok && da.Status == deviceStatusPending {
			da.Status = deviceStatusApproved
			da.Subject = subject
			da.Roles = roles
			approved = true
		}
		return nil
	})
	return approved && err == nil
}
func (d *deviceStore) deny(code string) bool {
	var denied bool
	err := d.transact(func() error {
		denied = false
		da, ok := d.byUser[code]
		if ok && da.Status == deviceStatusPending {
			da.Status = deviceStatusDenied
			denied = true
		}
		return nil
	})
	return denied && err == nil
}
func (d *deviceStore) delete(code string) {
	_ = d.transact(func() error {
		if da, ok := d.byDevice[code]; ok {
			d.removeLocked(da)
		}
		return nil
	})
}
func (d *deviceStore) removeLocked(da *deviceAuth) {
	delete(d.byDevice, da.DeviceCode)
	delete(d.byUser, da.UserCode)
}

// poll atomically consumes terminal grants and enforces pending polling intervals.
func (d *deviceStore) poll(code string) (deviceAuth, string, error) {
	var out deviceAuth
	var failure string
	err := d.transact(func() error {
		failure = ""
		out = deviceAuth{}
		da, ok := d.byDevice[code]
		if !ok {
			failure = "expired_token"
			return nil
		}
		if da.Status == deviceStatusPending {
			if da.PollInterval == 0 {
				da.PollInterval = devicePollInterval * time.Second
			}
			if !da.LastPoll.IsZero() && d.now().Sub(da.LastPoll) < da.PollInterval {
				da.PollInterval += 5 * time.Second
				da.LastPoll = d.now()
				failure = "slow_down"
				return nil
			}
			da.LastPoll = d.now()
		} else {
			d.removeLocked(da)
		}
		out = *da
		return nil
	})
	return out, failure, err
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
