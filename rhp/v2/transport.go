package rhp

import (
	"bytes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.sia.tech/core/types"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/poly1305"
	"lukechampine.com/frand"
)

// minMessageSize is the minimum size of an RPC message. If an encoded message
// would be smaller than minMessageSize, the sender MAY pad it with random data.
// This hinders traffic analysis by obscuring the true sizes of messages.
const minMessageSize = 4096

var (
	// Handshake specifiers
	loopEnter = types.NewSpecifier("LoopEnter")
	loopExit  = types.NewSpecifier("LoopExit")

	// RPC ciphers
	cipherChaCha20Poly1305 = types.NewSpecifier("ChaCha20Poly1305")
	cipherNoOverlap        = types.NewSpecifier("NoOverlap")

	// ErrRenterClosed is returned by (*Transport).ReadID when the renter sends the
	// Transport termination signal.
	ErrRenterClosed = errors.New("renter has terminated Transport")
)

// wrapResponseErr formats RPC response errors nicely, wrapping them in either
// readCtx or rejectCtx depending on whether we encountered an I/O error or the
// host sent an explicit error message.
func wrapResponseErr(err error, readCtx, rejectCtx string) error {
	if errors.As(err, new(*RPCError)) {
		return fmt.Errorf("%s: %w", rejectCtx, err)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", readCtx, err)
	}
	return nil
}

func generateX25519KeyPair() (xsk []byte, xpk [32]byte) {
	xsk = frand.Bytes(32)
	pk, _ := curve25519.X25519(xsk, curve25519.Basepoint)
	copy(xpk[:], pk)
	return
}

func deriveSharedSecret(xsk []byte, xpk [32]byte) ([]byte, error) {
	secret, err := curve25519.X25519(xsk, xpk[:])
	if err != nil {
		return nil, err
	}
	key := blake2b.Sum256(secret)
	return key[:], nil
}

// An RPCError may be sent instead of a response object to any RPC.
type RPCError struct {
	Type        types.Specifier
	Data        []byte // structure depends on Type
	Description string // human-readable error string
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	return e.Description
}

// Is reports whether this error matches target.
func (e *RPCError) Is(target error) bool {
	return strings.Contains(e.Description, target.Error())
}

// helper type for encoding and decoding RPC response messages, which can
// represent either valid data or an error.
type rpcResponse struct {
	err  *RPCError
	data ProtocolObject
}

// A Transport facilitates the exchange of RPCs via the renter-host protocol,
// version 2.
type Transport struct {
	conn      net.Conn
	aead      cipher.AEAD
	key       []byte // for RawResponse
	inbuf     bytes.Buffer
	outbuf    bytes.Buffer
	challenge [16]byte
	isRenter  bool
	hostKey   types.PublicKey

	mu     sync.Mutex
	r, w   uint64
	err    error // set when Transport is prematurely closed
	closed bool
}

func (t *Transport) setErr(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil && t.err == nil {
		if _, ok := err.(net.Error); !ok {
			t.conn.Close()
			t.err = err
		}
	}
}

// HostKey returns the host's public key.
func (t *Transport) HostKey() types.PublicKey { return t.hostKey }

// BytesRead returns the number of bytes read from the underlying connection.
func (t *Transport) BytesRead() uint64 { return atomic.LoadUint64(&t.r) }

// BytesWritten returns the number of bytes written to the underlying connection.
func (t *Transport) BytesWritten() uint64 { return atomic.LoadUint64(&t.w) }

// PrematureCloseErr returns the error that resulted in the Transport being closed
// prematurely.
func (t *Transport) PrematureCloseErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// IsClosed returns whether the Transport is closed. Check PrematureCloseErr to
// determine whether the Transport was closed gracefully.
func (t *Transport) IsClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed || t.err != nil
}

func hashChallenge(challenge [16]byte) [32]byte {
	c := make([]byte, 32)
	copy(c[:16], "challenge")
	copy(c[16:], challenge[:])
	return blake2b.Sum256(c)
}

// SetChallenge sets the current Transport challenge.
func (t *Transport) SetChallenge(challenge [16]byte) {
	t.challenge = challenge
}

// SetDeadline sets the read and write deadline on the underlying connection.
func (t *Transport) SetDeadline(deadline time.Time) {
	t.conn.SetDeadline(deadline)
}

// SetReadDeadline sets the deadline for future read calls on the underlying
// connection.
func (t *Transport) SetReadDeadline(deadline time.Time) {
	t.conn.SetReadDeadline(deadline)
}

// SetWriteDeadline sets the deadline for future write calls on the underlying
// connection.
func (t *Transport) SetWriteDeadline(deadline time.Time) {
	t.conn.SetWriteDeadline(deadline)
}

// SignChallenge signs the current Transport challenge.
func (t *Transport) SignChallenge(priv types.PrivateKey) types.Signature {
	return priv.SignHash(hashChallenge(t.challenge))
}

// VerifyChallenge verifies a challenge signature and returns a new challenge.
func (t *Transport) VerifyChallenge(sig types.Signature, pubkey types.PublicKey) ([16]byte, bool) {
	ok := pubkey.VerifyHash(hashChallenge(t.challenge), sig)
	if !ok {
		return [16]byte{}, false
	}
	t.challenge = frand.Entropy128()
	return t.challenge, true
}

func (t *Transport) writeMessage(obj ProtocolObject) error {
	if err := t.PrematureCloseErr(); err != nil {
		return err
	}
	nonce := make([]byte, 32)[:t.aead.NonceSize()] // avoid heap alloc
	frand.Read(nonce)

	t.outbuf.Reset()
	t.outbuf.Grow(minMessageSize)
	e := types.NewEncoder(&t.outbuf)
	e.WritePrefix(0) // placeholder
	e.Write(nonce)
	obj.EncodeTo(e)
	e.Flush()

	// overwrite message length
	msgSize := t.outbuf.Len() + t.aead.Overhead()
	if msgSize < minMessageSize {
		msgSize = minMessageSize
	}
	t.outbuf.Grow(t.aead.Overhead())
	msg := t.outbuf.Bytes()[:msgSize]
	binary.LittleEndian.PutUint64(msg[:8], uint64(msgSize-8))

	// encrypt the object in-place
	msgNonce := msg[8:][:len(nonce)]
	payload := msg[8+len(nonce) : msgSize-t.aead.Overhead()]
	t.aead.Seal(payload[:0], msgNonce, payload, nil)

	n, err := t.conn.Write(msg)
	atomic.AddUint64(&t.w, uint64(n))
	t.setErr(err)
	return err
}

func (t *Transport) readMessage(obj ProtocolObject, maxLen uint64) error {
	if err := t.PrematureCloseErr(); err != nil {
		return err
	}
	if maxLen < minMessageSize {
		maxLen = minMessageSize
	}
	d := types.NewDecoder(io.LimitedReader{R: t.conn, N: int64(8 + maxLen)})
	msgSize := d.ReadUint64()
	if d.Err() != nil {
		return d.Err()
	} else if msgSize > maxLen {
		return fmt.Errorf("message size (%v bytes) exceeds maxLen of %v bytes", msgSize, maxLen)
	} else if msgSize < uint64(t.aead.NonceSize()+t.aead.Overhead()) {
		return fmt.Errorf("message size (%v bytes) is too small (nonce + MAC is %v bytes)", msgSize, t.aead.NonceSize()+t.aead.Overhead())
	}
	t.inbuf.Reset()
	t.inbuf.Grow(int(msgSize))
	buf := t.inbuf.Bytes()[:msgSize]
	d.Read(buf)
	if d.Err() != nil {
		return d.Err()
	}
	atomic.AddUint64(&t.r, uint64(8+msgSize))

	nonce := buf[:t.aead.NonceSize()]
	paddedPayload := buf[t.aead.NonceSize():]
	plaintext, err := t.aead.Open(paddedPayload[:0], nonce, paddedPayload, nil)
	if err != nil {
		t.setErr(err) // not an I/O error, but still fatal
		return err
	}
	d = types.NewBufDecoder(plaintext)
	obj.DecodeFrom(d)
	return d.Err()
}

// WriteRequest sends an encrypted RPC request, comprising an RPC ID and a
// request object.
func (t *Transport) WriteRequest(rpcID types.Specifier, req ProtocolObject) error {
	if err := t.writeMessage(&rpcID); err != nil {
		return fmt.Errorf("WriteRequestID: %w", err)
	}
	if req != nil {
		if err := t.writeMessage(req); err != nil {
			return fmt.Errorf("WriteRequest: %w", err)
		}
	}
	return nil
}

// ReadID reads an RPC request ID. If the renter sends the Transport termination
// signal, ReadID returns ErrRenterClosed.
func (t *Transport) ReadID() (rpcID types.Specifier, err error) {
	defer wrapErr(&err, "ReadID")
	err = t.readMessage(&rpcID, minMessageSize)
	if rpcID == loopExit {
		err = ErrRenterClosed
	}
	return
}

// ReadRequest reads an RPC request using the new loop protocol.
func (t *Transport) ReadRequest(req ProtocolObject, maxLen uint64) (err error) {
	defer wrapErr(&err, "ReadRequest")
	return t.readMessage(req, maxLen)
}

// WriteResponse writes an RPC response object.
func (t *Transport) WriteResponse(resp ProtocolObject) (e error) {
	defer wrapErr(&e, "WriteResponse")
	return t.writeMessage(&rpcResponse{nil, resp})
}

// WriteResponseErr writes an error. If err is an *RPCError, it is sent
// directly; otherwise, a generic RPCError is created from err's Error string.
func (t *Transport) WriteResponseErr(err error) (e error) {
	defer wrapErr(&e, "WriteResponseErr")
	re, ok := err.(*RPCError)
	if err != nil && !ok {
		re = &RPCError{Description: err.Error()}
	}
	return t.writeMessage(&rpcResponse{re, nil})
}

// ReadResponse reads an RPC response. If the response is an error, it is
// returned directly.
func (t *Transport) ReadResponse(resp ProtocolObject, maxLen uint64) (err error) {
	defer wrapErr(&err, "ReadResponse")
	rr := rpcResponse{nil, resp}
	if err := t.readMessage(&rr, maxLen); err != nil {
		return err
	} else if rr.err != nil {
		return rr.err
	}
	return nil
}

// Call is a helper method that writes a request and then reads a response.
func (t *Transport) Call(rpcID types.Specifier, req, resp ProtocolObject) error {
	if err := t.WriteRequest(rpcID, req); err != nil {
		return err
	}
	// use a maxlen large enough for all RPCs except Read, Write, and
	// SectorRoots (which don't use Call anyway)
	err := t.ReadResponse(resp, 4096)
	return wrapResponseErr(err, fmt.Sprintf("couldn't read %v response", rpcID), fmt.Sprintf("host rejected %v request", rpcID))
}

// A ResponseReader contains an unencrypted, unauthenticated RPC response
// message.
type ResponseReader struct {
	msgR   io.Reader
	tagR   io.Reader
	mac    *poly1305.MAC
	clen   uint64
	setErr func(error)
}

// Read implements io.Reader.
func (rr *ResponseReader) Read(p []byte) (int, error) {
	n, err := rr.msgR.Read(p)
	if err != io.EOF {
		// EOF is expected, since this is a limited reader
		rr.setErr(err)
	}
	return n, err
}

// VerifyTag verifies the authentication tag appended to the message. VerifyTag
// must be called after Read returns io.EOF, and the message must be discarded
// if VerifyTag returns a non-nil error.
func (rr *ResponseReader) VerifyTag() error {
	// the caller may not have consumed the full message (e.g. if it was padded
	// to minMessageSize), so make sure the whole thing is written to the MAC
	if _, err := io.Copy(io.Discard, rr); err != nil {
		return err
	}

	var tag [poly1305.TagSize]byte
	if _, err := io.ReadFull(rr.tagR, tag[:]); err != nil {
		rr.setErr(err)
		return err
	}
	// MAC is padded to 16 bytes, and covers the length of AD (0 in this case)
	// and ciphertext
	tail := make([]byte, 0, 32)[:32-(rr.clen%16)]
	binary.LittleEndian.PutUint64(tail[len(tail)-8:], rr.clen)
	rr.mac.Write(tail)
	var ourTag [poly1305.TagSize]byte
	rr.mac.Sum(ourTag[:0])
	if subtle.ConstantTimeCompare(tag[:], ourTag[:]) != 1 {
		err := errors.New("chacha20poly1305: message authentication failed")
		rr.setErr(err) // not an I/O error, but still fatal
		return err
	}
	return nil
}

// RawResponse returns a stream containing the (unencrypted, unauthenticated)
// content of the next message. The Reader must be fully consumed by the caller,
// after which the caller should call VerifyTag to authenticate the message. If
// the response was an RPCError, it is authenticated and returned immediately.
func (t *Transport) RawResponse(maxLen uint64) (*ResponseReader, error) {
	if maxLen < minMessageSize {
		maxLen = minMessageSize
	}
	d := types.NewDecoder(io.LimitedReader{R: t.conn, N: int64(8 + chacha20.NonceSize)})
	msgSize := d.ReadUint64()
	if msgSize > maxLen {
		return nil, fmt.Errorf("message size (%v bytes) exceeds maxLen of %v bytes", msgSize, maxLen)
	} else if msgSize < uint64(chacha20.NonceSize+poly1305.TagSize) {
		return nil, fmt.Errorf("message size (%v bytes) is too small (nonce + MAC is %v bytes)", msgSize, chacha20.NonceSize+poly1305.TagSize)
	}
	msgSize -= uint64(chacha20.NonceSize + poly1305.TagSize)

	nonce := make([]byte, 32)[:chacha20.NonceSize] // avoid heap allocation
	d.Read(nonce)

	// construct reader
	c, _ := chacha20.NewUnauthenticatedCipher(t.key, nonce)
	var polyKey [32]byte
	c.XORKeyStream(polyKey[:], polyKey[:])
	mac := poly1305.New(&polyKey)
	c.SetCounter(1)
	rr := &ResponseReader{
		msgR: cipher.StreamReader{
			R: io.TeeReader(io.LimitReader(t.conn, int64(msgSize)), mac),
			S: c,
		},
		tagR:   io.LimitReader(t.conn, poly1305.TagSize),
		mac:    mac,
		clen:   msgSize,
		setErr: t.setErr,
	}

	// check if response is an RPCError
	d = types.NewDecoder(io.LimitedReader{R: rr, N: int64(msgSize)})
	if isErr := d.ReadBool(); isErr {
		err := new(RPCError)
		err.DecodeFrom(d)
		if err := rr.VerifyTag(); err != nil {
			return nil, err
		}
		return nil, err
	}
	// not an error; pass rest of stream to caller
	return rr, nil
}

// Close gracefully terminates the RPC loop and closes the connection.
func (t *Transport) Close() (err error) {
	defer wrapErr(&err, "Close")
	if t.IsClosed() {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	if t.isRenter {
		t.SetWriteDeadline(time.Now().Add(time.Second))
		t.writeMessage(&loopExit)
	}
	return t.conn.Close()
}

// ForceClose calls Close on the transport's underlying connection.
func (t *Transport) ForceClose() (err error) {
	defer wrapErr(&err, "ForceClose")
	if t.IsClosed() {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return t.conn.Close()
}

func hashKeys(k1, k2 [32]byte) types.Hash256 {
	return blake2b.Sum256(append(append(make([]byte, 0, len(k1)+len(k2)), k1[:]...), k2[:]...))
}

// NewHostTransport conducts the hosts's half of the renter-host protocol
// handshake, returning a Transport that can be used to handle RPC requests.
func NewHostTransport(conn net.Conn, priv types.PrivateKey) (_ *Transport, err error) {
	defer wrapErr(&err, "NewHostTransport")
	e := types.NewEncoder(conn)
	d := types.NewDecoder(io.LimitedReader{R: conn, N: 1024})

	var req loopKeyExchangeRequest
	req.DecodeFrom(d)
	if err := d.Err(); err != nil {
		return nil, err
	}

	var supportsChaCha bool
	for _, c := range req.Ciphers {
		if c == cipherChaCha20Poly1305 {
			supportsChaCha = true
		}
	}
	if !supportsChaCha {
		(&loopKeyExchangeResponse{Cipher: cipherNoOverlap}).EncodeTo(e)
		return nil, errors.New("no supported ciphers")
	}

	xsk, xpk := generateX25519KeyPair()
	h := hashKeys(req.PublicKey, xpk)
	resp := loopKeyExchangeResponse{
		Cipher:    cipherChaCha20Poly1305,
		PublicKey: xpk,
		Signature: priv.SignHash(h),
	}
	resp.EncodeTo(e)
	if err := e.Flush(); err != nil {
		return nil, err
	}

	cipherKey, err := deriveSharedSecret(xsk, req.PublicKey)
	if err != nil {
		return nil, err
	}
	aead, _ := chacha20poly1305.New(cipherKey) // no error possible
	t := &Transport{
		conn:      conn,
		aead:      aead,
		key:       cipherKey,
		challenge: frand.Entropy128(),
		isRenter:  false,
		hostKey:   priv.PublicKey(),
	}
	// hack: cast challenge to Specifier to make it a ProtocolObject
	if err := t.writeMessage((*types.Specifier)(&t.challenge)); err != nil {
		return nil, err
	}
	return t, nil
}

// NewRenterTransport conducts the renter's half of the renter-host protocol
// handshake, returning a Transport that can be used to make RPC requests.
func NewRenterTransport(conn net.Conn, pub types.PublicKey) (_ *Transport, err error) {
	defer wrapErr(&err, "NewRenterTransport")
	e := types.NewEncoder(conn)
	d := types.NewDecoder(io.LimitedReader{R: conn, N: 1024})

	xsk, xpk := generateX25519KeyPair()
	req := &loopKeyExchangeRequest{
		PublicKey: xpk,
		Ciphers:   []types.Specifier{cipherChaCha20Poly1305},
	}
	req.EncodeTo(e)
	if err := e.Flush(); err != nil {
		return nil, fmt.Errorf("couldn't write handshake: %w", err)
	}
	var resp loopKeyExchangeResponse
	resp.DecodeFrom(d)
	if err := d.Err(); err != nil {
		return nil, fmt.Errorf("couldn't read host's handshake: %w", err)
	}
	// validate the signature before doing anything else
	h := hashKeys(req.PublicKey, resp.PublicKey)
	if !pub.VerifyHash(h, resp.Signature) {
		return nil, errors.New("host's handshake signature was invalid")
	}
	if resp.Cipher == cipherNoOverlap {
		return nil, errors.New("host does not support any of our proposed ciphers")
	} else if resp.Cipher != cipherChaCha20Poly1305 {
		return nil, errors.New("host selected unsupported cipher")
	}

	cipherKey, err := deriveSharedSecret(xsk, resp.PublicKey)
	if err != nil {
		return nil, err
	}
	aead, _ := chacha20poly1305.New(cipherKey) // no error possible
	t := &Transport{
		conn:     conn,
		aead:     aead,
		key:      cipherKey,
		isRenter: true,
		hostKey:  pub,
	}
	// hack: cast challenge to Specifier to make it a ProtocolObject
	if err := t.readMessage((*types.Specifier)(&t.challenge), minMessageSize); err != nil {
		return nil, err
	}
	return t, nil
}

// Handshake objects
type (
	loopKeyExchangeRequest struct {
		PublicKey [32]byte
		Ciphers   []types.Specifier
	}

	loopKeyExchangeResponse struct {
		PublicKey [32]byte
		Signature types.Signature
		Cipher    types.Specifier
	}
)
