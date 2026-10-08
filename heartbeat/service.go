/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package heartbeat

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/rpc"
)

// Config selects the endpoint, the node key, and where heads come from.
type Config struct {
	Address     string
	URL         string
	Kind        string
	Client      string
	Key         *ecdsa.PrivateKey
	Tips        func() []Tip
	Log         *log.ScdoLog
	HTTP        *http.Client
	Interval    time.Duration
	Jitter      time.Duration
	BackoffBase time.Duration
	BackoffCap  time.Duration
	Now         func() time.Time
}

// Status is the last server response, exposed to RPC and the phone UI.
// Share and pool strings are copied from the server. This process does not
// calculate them.
type Status struct {
	Enabled       bool    `json:"enabled"`
	Kind          string  `json:"kind"`
	NodeID        string  `json:"node_id"`
	PayoutAddress string  `json:"payout_address"`
	Client        string  `json:"client"`
	OK            bool    `json:"ok"`
	EligibleToday bool    `json:"eligible_today"`
	UptimeToday   float64 `json:"uptime_today"`
	Weight        float64 `json:"weight"`
	EstShareSCDO  string  `json:"est_share_scdo"`
	PoolTodaySCDO string  `json:"pool_today_scdo"`
	Reason        string  `json:"reason"`
	Error         string  `json:"error,omitempty"`
	LastAttempt   int64   `json:"last_attempt"`
	NextAttempt   int64   `json:"next_attempt"`
}

// serverResponse is the heartbeat POST body. Numbers the server owns stay
// strings so this client never turns them into a local reward.
type serverResponse struct {
	OK            bool       `json:"ok"`
	EligibleToday bool       `json:"eligible_today"`
	UptimeToday   float64    `json:"uptime_today"`
	Weight        float64    `json:"weight"`
	EstShareSCDO  flexString `json:"est_share_scdo"`
	PoolTodaySCDO flexString `json:"pool_today_scdo"`
	Reason        *string    `json:"reason"`
}

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" || len(b) == 0 {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(b)
	return nil
}

// Service posts heartbeats and serves scdo_rewardStatus.
type Service struct {
	mu          sync.Mutex
	address     string
	url         string
	kind        string
	client      string
	key         *ecdsa.PrivateKey
	nodeID      string
	tips        func() []Tip
	log         *log.ScdoLog
	http        *http.Client
	interval    time.Duration
	jitter      time.Duration
	backoffBase time.Duration
	backoffCap  time.Duration
	now         func() time.Time
	rnd         *rand.Rand

	started bool
	cancel  context.CancelFunc
	done    chan struct{}
	wake    chan struct{}
	status  Status
}

// New validates the address and URL. An empty pair leaves the service off.
func New(cfg Config) (*Service, error) {
	address, err := NormalizeAddress(cfg.Address)
	if err != nil {
		return nil, err
	}
	endpoint, err := NormalizeURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	kind := cfg.Kind
	if kind == "" {
		kind = KindFull
	}
	if kind != KindLight && kind != KindFull {
		return nil, fmt.Errorf("reward heartbeat kind must be light or full")
	}
	name := cfg.Client
	if name == "" {
		name = ClientGoSCDO
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = Interval
	}
	jitter := cfg.Jitter
	if cfg.Interval <= 0 && cfg.Jitter == 0 {
		jitter = Jitter
	}
	base := cfg.BackoffBase
	if base <= 0 {
		base = BackoffBase
	}
	capWait := cfg.BackoffCap
	if capWait <= 0 {
		capWait = BackoffCap
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: PostTimeout, CheckRedirect: refuseRedirect}
	}
	var nodeID string
	if cfg.Key != nil {
		nodeID, err = NodeID(cfg.Key)
		if err != nil {
			return nil, err
		}
	}
	tips := cfg.Tips
	if tips == nil {
		tips = func() []Tip { return nil }
	}
	svc := &Service{
		address:     address,
		url:         endpoint,
		kind:        kind,
		client:      name,
		key:         cfg.Key,
		nodeID:      nodeID,
		tips:        tips,
		log:         cfg.Log,
		http:        client,
		interval:    interval,
		jitter:      jitter,
		backoffBase: base,
		backoffCap:  capWait,
		now:         now,
		rnd:         rand.New(rand.NewSource(now().UnixNano())),
		wake:        make(chan struct{}, 1),
		status: Status{
			Enabled:       Enabled(address, endpoint),
			Kind:          kind,
			NodeID:        nodeID,
			PayoutAddress: address,
			Client:        name,
		},
	}
	return svc, nil
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// Protocols implements node.Service. The heartbeat does not speak p2p.
func (s *Service) Protocols() []p2p.Protocol { return nil }

// APIs implements node.Service. The method is scdo_rewardStatus.
func (s *Service) APIs() []rpc.API {
	return []rpc.API{{
		Namespace: "scdo",
		Version:   "1.0",
		Service:   &API{svc: s},
		Public:    true,
	}}
}

// Start implements node.Service. The loop does nothing until both the
// payout address and the URL are set.
func (s *Service) Start(server *p2p.Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if Enabled(s.address, s.url) && s.key == nil {
		return fmt.Errorf("reward heartbeat needs the node p2p key")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.started = true
	s.status.Enabled = Enabled(s.address, s.url)
	go s.run(ctx, s.done)
	s.logStateLocked()
	return nil
}

// Stop implements node.Service.
func (s *Service) Stop() error {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.started = false
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(PostTimeout + time.Second):
		}
	}
	if s.http != nil {
		s.http.CloseIdleConnections()
	}
	return nil
}

// Apply changes the payout address and the endpoint. An empty value turns
// that side off. The node key does not change.
func (s *Service) Apply(address, url string) error {
	address, err := NormalizeAddress(address)
	if err != nil {
		return err
	}
	url, err = NormalizeURL(url)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if Enabled(address, url) && s.key == nil {
		return fmt.Errorf("reward heartbeat needs the node p2p key")
	}
	s.address = address
	s.url = url
	s.status.Enabled = Enabled(address, url)
	s.status.PayoutAddress = address
	if s.started {
		s.logStateLocked()
		s.poke()
	}
	return nil
}

// Status returns a copy of the last result.
func (s *Service) Status() Status {
	if s == nil {
		return Status{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// API is the RPC receiver. Its name is exported so the node can register it.
type API struct {
	svc *Service
}

// RewardStatus is scdo_rewardStatus.
func (a *API) RewardStatus() (Status, error) {
	if a == nil || a.svc == nil {
		return Status{}, nil
	}
	return a.svc.Status(), nil
}

func (s *Service) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if !s.snapshotEnabled() {
			failures = 0
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			}
			continue
		}
		wait := s.nextWait(failures)
		s.setNext(s.now().Add(wait))
		if !s.sleep(ctx, wait) {
			return
		}
		if !s.snapshotEnabled() {
			continue
		}
		if err := s.post(ctx); err != nil {
			failures++
			s.noteError(err)
			s.warn("reward heartbeat failed: %s", err)
			continue
		}
		failures = 0
	}
}

func (s *Service) snapshotEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started && Enabled(s.address, s.url)
}

func (s *Service) nextWait(failures int) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if failures > 0 {
		// Backoff is not jittered. A ±30s spread on the first 30s retry
		// could otherwise fire again immediately.
		return Backoff(failures, s.backoffBase, s.backoffCap)
	}
	wait := Spread(s.interval, s.jitter, s.rnd.Float64())
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return wait
}

func (s *Service) sleep(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
		return ctx.Err() == nil
	case <-timer.C:
		return true
	}
}

func (s *Service) post(ctx context.Context) error {
	address, endpoint, kind, name, key := s.snapshot()
	if key == nil {
		return fmt.Errorf("node key is missing")
	}
	msg := Message{
		V:             Version,
		NodeID:        s.nodeID,
		Kind:          kind,
		Shards:        ShardsFromTips(s.tips()),
		PayoutAddress: address,
		Client:        name,
		TS:            s.now().Unix(),
	}
	canonical, err := CanonicalJSON(msg)
	if err != nil {
		return err
	}
	sig, err := SignNodeKey(key, canonical)
	if err != nil {
		return err
	}
	recovered, err := crypto.Ecrecover(crypto.Keccak256(canonical), sig)
	if err != nil {
		return err
	}
	if !bytes.Equal(recovered, crypto.FromECDSAPub(&key.PublicKey)) {
		return fmt.Errorf("heartbeat signature does not recover the node key")
	}
	body := AttachSig(canonical, sig)
	call, cancel := context.WithTimeout(ctx, PostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(call, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat http %d", resp.StatusCode)
	}
	var parsed serverResponse
	if err = json.Unmarshal(payload, &parsed); err != nil {
		return fmt.Errorf("heartbeat response: %s", err)
	}
	reason := ""
	if parsed.Reason != nil {
		reason = *parsed.Reason
	}
	s.record(parsed, reason)
	s.info("reward heartbeat eligible=%t uptime=%g est_share=%s", parsed.EligibleToday, parsed.UptimeToday, string(parsed.EstShareSCDO))
	return nil
}

func (s *Service) snapshot() (address, endpoint, kind, name string, key *ecdsa.PrivateKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.address, s.url, s.kind, s.client, s.key
}

func (s *Service) record(parsed serverResponse, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Enabled = Enabled(s.address, s.url)
	s.status.Kind = s.kind
	s.status.NodeID = s.nodeID
	s.status.PayoutAddress = s.address
	s.status.Client = s.client
	s.status.OK = parsed.OK
	s.status.EligibleToday = parsed.EligibleToday
	s.status.UptimeToday = parsed.UptimeToday
	s.status.Weight = parsed.Weight
	s.status.EstShareSCDO = string(parsed.EstShareSCDO)
	s.status.PoolTodaySCDO = string(parsed.PoolTodaySCDO)
	s.status.Reason = reason
	s.status.Error = ""
	s.status.LastAttempt = s.now().Unix()
}

func (s *Service) noteError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.OK = false
	s.status.Error = err.Error()
	s.status.LastAttempt = s.now().Unix()
}

func (s *Service) setNext(at time.Time) {
	s.mu.Lock()
	s.status.NextAttempt = at.Unix()
	s.mu.Unlock()
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) logStateLocked() {
	if Enabled(s.address, s.url) {
		s.info("reward heartbeat on, payout %s", s.address)
		return
	}
	s.info("reward heartbeat off")
}

func (s *Service) info(format string, args ...interface{}) {
	if s.log != nil {
		s.log.Info(format, args...)
	}
}

func (s *Service) warn(format string, args ...interface{}) {
	if s.log != nil {
		s.log.Warn(format, args...)
	}
}
