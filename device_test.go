package glwm2m

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/maxid/gnety/codec/coap"
)

// fakeDevice is a LwM2M client that uses one UDP socket for both roles: it
// registers as a CoAP client and serves the object tree as a CoAP server at the
// address it registered from, which is what a real device does and what the
// downlink dialer assumes.
type fakeDevice struct {
	t    *testing.T
	conn *net.UDPConn
	addr string
	peer *net.UDPAddr // the LwM2M server
	seq  uint32
	mid  uint32

	mu        sync.Mutex
	resources map[string]string
	pending   map[string]chan coap.Message
	observes  map[string]*fakeObserve
	executed  []string
	closed    bool

	done chan struct{}
	wg   sync.WaitGroup
}

type fakeObserve struct {
	token []byte
	path  string
	peer  *net.UDPAddr
}

func newFakeDevice(t *testing.T, resources map[string]string) *fakeDevice {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(resources))
	for path, value := range resources {
		values[path] = value
	}
	d := &fakeDevice{
		t: t, conn: conn, addr: conn.LocalAddr().String(),
		resources: values,
		pending:   make(map[string]chan coap.Message),
		observes:  make(map[string]*fakeObserve),
		done:      make(chan struct{}),
	}
	d.wg.Add(1)
	go d.loop()
	t.Cleanup(d.close)
	return d
}

func (d *fakeDevice) close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	d.mu.Unlock()
	close(d.done)
	_ = d.conn.Close()
	d.wg.Wait()
}

func (d *fakeDevice) nextMID() uint16 {
	d.mid++
	return uint16(d.mid)
}

func (d *fakeDevice) nextToken() []byte {
	d.mid++
	return []byte{byte(d.mid), byte(d.mid >> 8)}
}

// request sends one CoAP request and waits for the response that carries its
// token.
func (d *fakeDevice) request(ctx context.Context, msg coap.Message) (coap.Message, error) {
	if d.peer == nil {
		return coap.Message{}, errors.New("fake device is not registered")
	}
	key := string(msg.Token)
	ch := make(chan coap.Message, 1)
	d.mu.Lock()
	d.pending[key] = ch
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
	}()
	raw, err := msg.Marshal()
	if err != nil {
		return coap.Message{}, err
	}
	if _, err := d.conn.WriteToUDP(raw, d.peer); err != nil {
		return coap.Message{}, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return coap.Message{}, ctx.Err()
	}
}

// register performs POST /rd and stores the server address for notifications.
func (d *fakeDevice) register(ctx context.Context, serverAddr, endpoint string) (string, error) {
	peer, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.peer = peer
	d.mu.Unlock()
	msg := coap.Message{Type: coap.Confirmable, Code: coap.CodePOST, MessageID: d.nextMID(), Token: d.nextToken()}
	if err := msg.SetURIPath("rd"); err != nil {
		return "", err
	}
	if err := msg.SetURIQuery("ep="+endpoint, "lt=300", "b=U", "lwm2m=1.1"); err != nil {
		return "", err
	}
	msg.Payload = []byte("</3303/0>,</3/0>")
	resp, err := d.request(ctx, msg)
	if err != nil {
		return "", err
	}
	if resp.Code != coap.CodeCreated {
		return "", fmt.Errorf("register answered %s", resp.Code)
	}
	var id string
	for _, option := range resp.Options {
		if option.Number == coap.OptionURIPath && string(option.Value) != "rd" {
			id = string(option.Value)
		}
	}
	return id, nil
}

// notify pushes one NON notification for an active observation.
func (d *fakeDevice) notify(path, value string) {
	d.mu.Lock()
	var target *fakeObserve
	for _, observe := range d.observes {
		if observe.path == path {
			target = observe
			break
		}
	}
	d.seq++
	seq := d.seq
	d.mu.Unlock()
	if target == nil || target.peer == nil {
		return
	}
	msg := coap.Message{
		Type: coap.NonConfirmable, Code: coap.CodeContent, MessageID: d.nextMID(),
		Token:   append([]byte(nil), target.token...),
		Payload: []byte(value),
		Options: []coap.Option{{Number: coap.OptionContentFormat, Value: []byte{0}}},
	}
	if err := msg.SetObserve(coap.Observe(seq)); err != nil {
		return
	}
	raw, err := msg.Marshal()
	if err != nil {
		return
	}
	_, _ = d.conn.WriteToUDP(raw, target.peer)
}

// value returns the current value of a resource path.
func (d *fakeDevice) value(path string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	value, ok := d.resources[path]
	return value, ok
}

// executeCount returns how many Execute requests arrived for a path.
func (d *fakeDevice) executeCount(path string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, executed := range d.executed {
		if executed == path {
			count++
		}
	}
	return count
}

func (d *fakeDevice) loop() {
	defer d.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg, err := coap.Parse(buf[:n])
		if err != nil {
			continue
		}
		if msg.Code.IsResponse() {
			d.mu.Lock()
			ch := d.pending[string(msg.Token)]
			d.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
			continue
		}
		d.handle(msg, from)
	}
}

func (d *fakeDevice) handle(req coap.Message, from *net.UDPAddr) {
	path := "/" + joinSegments(req.URIPath())

	switch {
	case req.Code == coap.CodePOST && path == "/rd":
		d.respond(req, from, coap.CodeCreated, nil,
			coap.Option{Number: coap.OptionURIPath, Value: []byte("rd")},
			coap.Option{Number: coap.OptionURIPath, Value: []byte("device")},
		)
	case req.Code == coap.CodePOST && len(req.URIPath()) > 0 && req.URIPath()[0] == "rd":
		d.respond(req, from, coap.CodeChanged, nil)
	case req.Code == coap.CodeDELETE && len(req.URIPath()) > 0 && req.URIPath()[0] == "rd":
		d.respond(req, from, coap.CodeDeleted, nil)
	case req.Code == coap.CodeGET && len(req.URIPath()) > 0 && req.URIPath()[0] == "objects":
		d.handleRead(req, from, path)
	case req.Code == coap.CodePUT && len(req.URIPath()) > 0 && req.URIPath()[0] == "objects":
		d.mu.Lock()
		d.resources[path] = string(req.Payload)
		d.mu.Unlock()
		d.respond(req, from, coap.CodeChanged, nil)
	case req.Code == coap.CodePOST && len(req.URIPath()) > 0 && req.URIPath()[0] == "objects":
		d.mu.Lock()
		d.executed = append(d.executed, path)
		d.mu.Unlock()
		d.respond(req, from, coap.CodeChanged, nil)
	default:
		d.respond(req, from, coap.CodeNotFound, nil)
	}
}

func (d *fakeDevice) handleRead(req coap.Message, from *net.UDPAddr, path string) {
	observe, hasObserve := req.Observe()
	d.mu.Lock()
	value, ok := d.resources[path]
	switch {
	case hasObserve && observe == coap.ObserveRegister:
		d.observes[string(req.Token)] = &fakeObserve{
			token: append([]byte(nil), req.Token...), path: path,
			peer: &net.UDPAddr{IP: from.IP, Port: from.Port},
		}
	case hasObserve && observe == coap.ObserveDeregister:
		delete(d.observes, string(req.Token))
	}
	d.seq++
	seq := d.seq
	d.mu.Unlock()
	if !ok {
		d.respond(req, from, coap.CodeNotFound, nil)
		return
	}
	options := []coap.Option{{Number: coap.OptionContentFormat, Value: []byte{0}}}
	if hasObserve && observe == coap.ObserveRegister {
		if err := setObserveOption(&options, seq); err == nil {
			d.respond(req, from, coap.CodeContent, []byte(value), options...)
			return
		}
	}
	d.respond(req, from, coap.CodeContent, []byte(value), options...)
}

func setObserveOption(options *[]coap.Option, seq uint32) error {
	msg := coap.Message{}
	if err := msg.SetObserve(coap.Observe(seq)); err != nil {
		return err
	}
	value, ok := msg.OptionValue(coap.OptionObserve)
	if !ok {
		return errors.New("missing observe option")
	}
	*options = append(*options, coap.Option{Number: coap.OptionObserve, Value: value})
	return nil
}

func (d *fakeDevice) respond(req coap.Message, from *net.UDPAddr, code coap.Code, payload []byte, options ...coap.Option) {
	out := coap.Message{
		Type: coap.Acknowledgement, Code: code, MessageID: req.MessageID,
		Token: append([]byte(nil), req.Token...), Options: options, Payload: payload,
	}
	if req.Type == coap.NonConfirmable {
		out.Type = coap.NonConfirmable
		out.MessageID = d.nextMID()
	}
	raw, err := out.Marshal()
	if err != nil {
		return
	}
	_, _ = d.conn.WriteToUDP(raw, from)
}

func joinSegments(segments []string) string {
	out := ""
	for i, segment := range segments {
		if i > 0 {
			out += "/"
		}
		out += segment
	}
	return out
}
