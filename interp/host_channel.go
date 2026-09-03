package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrHostChannelClosed is returned when the host closes one side of a bridge.
var ErrHostChannelClosed = errors.New("nanogo: host channel closed")

// HostChannel is a bidirectional, cancellation-aware bridge between a Go host
// and guest code. BindHostChannel exposes its two endpoints as separate guest
// channel variables: input is host -> nanoGo, output is nanoGo -> host.
//
// The host owns closure. Guest code cannot close either endpoint, so untrusted
// code cannot tear down the transport or send after a close race.
type HostChannel struct {
	inbound  chan any
	outbound chan any

	inputDone  chan struct{}
	outputDone chan struct{}
	inputOnce  sync.Once
	outputOnce sync.Once
}

// NewHostChannel creates a bridge with the same buffer capacity in both
// directions. A buffer of zero creates synchronous backpressure.
func NewHostChannel(buffer int) *HostChannel {
	if buffer < 0 {
		buffer = 0
	}
	return &HostChannel{
		inbound:    make(chan any, buffer),
		outbound:   make(chan any, buffer),
		inputDone:  make(chan struct{}),
		outputDone: make(chan struct{}),
	}
}

// Send transfers a supported value from the Go host to nanoGo. Primitive
// values, maps, and slices are copied into nanoGo runtime values; pointers,
// functions, structs, and other host capabilities are rejected rather than
// leaked to guest code.
func (c *HostChannel) Send(ctx context.Context, value any) error {
	if c == nil || channelDone(c.inputDone) {
		return ErrHostChannelClosed
	}
	v, err := bridgeToGuest(value)
	if err != nil {
		return err
	}
	return c.sendGuest(ctx, v)
}

// bridgeBatchToGuest converts non-scalar values before sending while allowing
// the common scalar event stream to reuse the caller's slice directly. The
// scalar set is immutable by value, so it is already safe to hand to the
// guest; slices and maps still go through bridgeToGuest and therefore retain
// the normal deep-copy boundary.
func bridgeBatchToGuest(values []any) ([]any, error) {
	var converted []any
	for i, value := range values {
		switch value.(type) {
		case nil, bool, string, int, int64, float64:
			continue
		}
		guestValue, err := bridgeToGuest(value)
		if err != nil {
			return nil, err
		}
		if converted == nil {
			converted = append([]any(nil), values...)
		}
		converted[i] = guestValue
	}
	if converted == nil {
		return values, nil
	}
	return converted, nil
}

func (c *HostChannel) sendGuestWithDone(ctx context.Context, ctxDone <-chan struct{}, value any) error {
	if ctxDone == nil {
		select {
		case c.inbound <- value:
			return nil
		case <-c.inputDone:
			return ErrHostChannelClosed
		}
	}
	select {
	case c.inbound <- value:
		return nil
	case <-c.inputDone:
		return ErrHostChannelClosed
	case <-ctxDone:
		return ctx.Err()
	}
}

// SendBatch converts and sends values in order. It validates and copies the
// complete batch before its first channel operation, so an unsupported value
// never leaves a partly accepted prefix behind. Sending itself remains
// sequential: a closed endpoint or cancelled context can therefore stop a
// batch after sent values, and the returned count reports that prefix.
//
// Batch transfer is useful for hosts that exchange bursts of small events. It
// preserves HostChannel's copying, backpressure, cancellation, and ownership
// rules while avoiding one public boundary call per event.
func (c *HostChannel) SendBatch(ctx context.Context, values []any) (int, error) {
	if c == nil || channelDone(c.inputDone) {
		return 0, ErrHostChannelClosed
	}
	converted, err := bridgeBatchToGuest(values)
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctxDone := ctx.Done()
	for i, value := range converted {
		if err := c.sendGuestWithDone(ctx, ctxDone, value); err != nil {
			return i, err
		}
	}
	return len(converted), nil
}

// sendGuest is Send after the caller has already made a safe guest copy.
func (c *HostChannel) sendGuest(ctx context.Context, value any) error {
	if c == nil || channelDone(c.inputDone) {
		return ErrHostChannelClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.sendGuestWithDone(ctx, ctx.Done(), value)
}

// Receive transfers the next guest value to the Go host as ordinary Go data.
func (c *HostChannel) Receive(ctx context.Context) (any, error) {
	if c == nil {
		return nil, ErrHostChannelClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Prefer already-queued output over the separate closure signal. Unlike a
	// raw closed Go channel, HostChannel leaves outbound open to avoid a host
	// send/close panic, so draining buffered values is explicit.
	select {
	case value := <-c.outbound:
		return bridgeToHost(value)
	default:
	}
	if channelDone(c.outputDone) {
		return nil, ErrHostChannelClosed
	}
	ctxDone := ctx.Done()
	if ctxDone == nil {
		select {
		case value := <-c.outbound:
			return bridgeToHost(value)
		case <-c.outputDone:
			// A value that was queued just before CloseOutput won the race must
			// still be observable before the endpoint reports closed.
			select {
			case value := <-c.outbound:
				return bridgeToHost(value)
			default:
			}
			return nil, ErrHostChannelClosed
		}
	}
	select {
	case value := <-c.outbound:
		return bridgeToHost(value)
	case <-c.outputDone:
		// A value that was queued just before CloseOutput won the race must
		// still be observable before the endpoint reports closed.
		select {
		case value := <-c.outbound:
			return bridgeToHost(value)
		default:
		}
		return nil, ErrHostChannelClosed
	case <-ctxDone:
		return nil, ctx.Err()
	}
}

// ReceiveBatch waits for one guest value, then drains up to max-1 further
// values that are already queued. It never waits for a later item, so a host
// event loop gets a bounded burst without turning its backpressure policy into
// an unbounded drain. max must be positive.
func (c *HostChannel) ReceiveBatch(ctx context.Context, max int) ([]any, error) {
	if max <= 0 {
		return nil, errors.New("nanogo: host channel batch size must be positive")
	}
	return c.receiveBatch(ctx, make([]any, 0, max), max)
}

// ReceiveBatchInto is ReceiveBatch for a host-owned reusable buffer. dst's
// capacity is the maximum burst size and its previous contents are discarded.
// Reusing the slice removes the allocation ReceiveBatch needs for its result,
// which is useful in long-lived event loops.
func (c *HostChannel) ReceiveBatchInto(ctx context.Context, dst []any) ([]any, error) {
	if cap(dst) == 0 {
		return nil, errors.New("nanogo: host channel batch buffer must have positive capacity")
	}
	return c.receiveBatch(ctx, dst[:0], cap(dst))
}

func (c *HostChannel) receiveBatch(ctx context.Context, values []any, max int) ([]any, error) {
	first, err := c.Receive(ctx)
	if err != nil {
		return nil, err
	}
	values = append(values, first)
	for len(values) < max {
		select {
		case value := <-c.outbound:
			converted, err := bridgeToHost(value)
			if err != nil {
				return values, err
			}
			values = append(values, converted)
		default:
			return values, nil
		}
	}
	return values, nil
}

// CloseInput stops host -> guest traffic. A guest receive observes a closed
// channel once buffered messages have been consumed.
func (c *HostChannel) CloseInput() {
	if c != nil {
		c.inputOnce.Do(func() { close(c.inputDone) })
	}
}

// CloseOutput stops guest -> host traffic and unblocks a guest sender. Values
// accepted into the output buffer before closure remain readable by Receive.
func (c *HostChannel) CloseOutput() {
	if c != nil {
		c.outputOnce.Do(func() { close(c.outputDone) })
	}
}

// Close closes both directions of the bridge. It is safe to call repeatedly.
func (c *HostChannel) Close() {
	c.CloseInput()
	c.CloseOutput()
}

// BindHostChannel adds two guest globals before execution starts. inputName is
// receive-only in guest code; outputName is send-only. This directionality is
// intentional: it prevents guest code from impersonating the host or stealing
// messages it just emitted.
func (vm *Interpreter) BindHostChannel(inputName, outputName string, channel *HostChannel) error {
	if channel == nil {
		return errors.New("nanogo: nil host channel")
	}
	if !validGuestIdentifier(inputName) || !validGuestIdentifier(outputName) || inputName == outputName {
		return errors.New("nanogo: host channel names must be distinct Go identifiers")
	}
	vm.runMu.Lock()
	defer vm.runMu.Unlock()
	if vm.execution.Load() != nil {
		return errors.New("nanogo: bind host channels before RunContext")
	}
	vm.declare(inputName, &ChannelVal{
		ElementType: "any", C: channel.inbound, done: channel.inputDone,
		hostOwned: true, direction: channelReceiveOnly,
	}, vm.globals)
	vm.declare(outputName, &ChannelVal{
		ElementType: "any", C: channel.outbound, done: channel.outputDone,
		hostOwned: true, direction: channelSendOnly,
	}, vm.globals)
	return nil
}

func validGuestIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	switch name {
	case "break", "default", "func", "interface", "select", "case", "defer", "go", "map", "struct", "chan", "else", "goto", "package", "switch", "const", "fallthrough", "if", "range", "type", "continue", "for", "import", "return", "var":
		return false
	}
	return true
}

func bridgeToGuest(value any) (any, error) {
	switch v := value.(type) {
	case nil, bool, string, int, int64, float64:
		return v, nil
	case []byte:
		out := &SliceVal{ElementType: "byte", Data: make([]any, len(v))}
		for i := range v {
			out.Data[i] = int(v[i])
		}
		return out, nil
	case []any:
		out := &SliceVal{ElementType: "any", Data: make([]any, len(v))}
		for i, item := range v {
			converted, err := bridgeToGuest(item)
			if err != nil {
				return nil, err
			}
			out.Data[i] = converted
		}
		return out, nil
	case []string:
		out := &SliceVal{ElementType: "string", Data: make([]any, len(v))}
		for i := range v {
			out.Data[i] = v[i]
		}
		return out, nil
	case []int:
		out := &SliceVal{ElementType: "int", Data: make([]any, len(v))}
		for i := range v {
			out.Data[i] = v[i]
		}
		return out, nil
	case []float64:
		out := &SliceVal{ElementType: "float64", Data: make([]any, len(v))}
		for i := range v {
			out.Data[i] = v[i]
		}
		return out, nil
	case []bool:
		out := &SliceVal{ElementType: "bool", Data: make([]any, len(v))}
		for i := range v {
			out.Data[i] = v[i]
		}
		return out, nil
	case map[string]any:
		out := &MapVal{KeyType: "string", ElementType: "any", Data: make(map[string]any, len(v))}
		for key, item := range v {
			converted, err := bridgeToGuest(item)
			if err != nil {
				return nil, err
			}
			// String-keyed maps use the raw key as their internal key, so the
			// bridge can bypass MapVal's dynamic-key compatibility machinery.
			out.Data[key] = converted
		}
		return out, nil
	case map[string]string:
		out := &MapVal{KeyType: "string", ElementType: "string", Data: make(map[string]any, len(v))}
		for key, value := range v {
			out.Data[key] = value
		}
		return out, nil
	case map[string]int:
		out := &MapVal{KeyType: "string", ElementType: "int", Data: make(map[string]any, len(v))}
		for key, value := range v {
			out.Data[key] = value
		}
		return out, nil
	default:
		return nil, fmt.Errorf("nanogo: unsupported host channel value %T", value)
	}
}

func bridgeToHost(value any) (any, error) {
	switch v := value.(type) {
	case nil, bool, string, int, int64, float64:
		return v, nil
	case *SliceVal:
		if v == nil {
			return nil, nil
		}
		// Preserve the natural host representation for scalar containers.
		// This avoids per-element recursive calls and interface boxing for the
		// byte/int/string payloads most tools exchange with nanoGo.
		switch v.ElementType {
		case "byte", "uint8":
			out := make([]byte, len(v.Data))
			for i, item := range v.Data {
				out[i] = byte(ToInt(item))
			}
			return out, nil
		case "int":
			out := make([]int, len(v.Data))
			for i, item := range v.Data {
				out[i] = ToInt(item)
			}
			return out, nil
		case "float64":
			out := make([]float64, len(v.Data))
			for i, item := range v.Data {
				out[i] = ToFloat(item)
			}
			return out, nil
		case "bool":
			out := make([]bool, len(v.Data))
			for i, item := range v.Data {
				out[i] = ToBool(item)
			}
			return out, nil
		case "string":
			out := make([]string, len(v.Data))
			for i, item := range v.Data {
				out[i] = ToString(item)
			}
			return out, nil
		}
		out := make([]any, len(v.Data))
		for i, item := range v.Data {
			converted, err := bridgeToHost(item)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	case *MapVal:
		out := make(map[string]any, len(v.Data))
		for hashed, item := range v.Data {
			converted, err := bridgeToHost(item)
			if err != nil {
				return nil, err
			}
			out[mapKeyToString(v.originalKey(hashed))] = converted
		}
		return out, nil
	case *StructVal:
		out := make(map[string]any, v.fieldCount())
		var bridgeErr error
		v.forEachField(func(name string, item any) {
			if bridgeErr != nil {
				return
			}
			if strings.HasPrefix(name, "__") {
				return
			}
			converted, err := bridgeToHost(item)
			if err != nil {
				bridgeErr = err
				return
			}
			out[name] = converted
		})
		if bridgeErr != nil {
			return nil, bridgeErr
		}
		return out, nil
	default:
		return nil, fmt.Errorf("nanogo: unsupported guest channel value %T", value)
	}
}

// BridgeToGuest converts an ordinary Go value into nanoGo's internal runtime
// representation (e.g. []int -> *SliceVal), the same conversion used at the
// host-channel boundary. Hosts building a data-driven function-call harness
// (see interp/loader's RunFunctionTest) use it to turn plain Go arguments
// into values callable functions accept.
func BridgeToGuest(value any) (any, error) { return bridgeToGuest(value) }

// BridgeToHost converts a nanoGo runtime value back into ordinary Go values
// (e.g. *SliceVal -> []byte, []int, []string, or []any), the inverse of
// BridgeToGuest while retaining concrete scalar slice types where possible.
func BridgeToHost(value any) (any, error) { return bridgeToHost(value) }
