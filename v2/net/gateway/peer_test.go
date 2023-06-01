package gateway

import (
	"errors"
	"net"
	"testing"

	"go.sia.tech/core/v2/net/rpc"
	"go.sia.tech/core/v2/types"
)

type objString string

func (s *objString) EncodeTo(e *types.Encoder)   { e.WriteString(string(*s)) }
func (s *objString) DecodeFrom(d *types.Decoder) { *s = objString(d.ReadString()) }
func (s *objString) MaxLen() int                 { return 100 }

func TestHandshake(t *testing.T) {
	genesisID := (&types.Block{}).ID()
	rpcGreet := rpc.NewSpecifier("greet")

	// initialize peer
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	peerErr := make(chan error, 1)
	go func() {
		peerErr <- func() error {
			conn, err := l.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			sess, err := AcceptSession(conn, genesisID, UniqueID{0})
			if err != nil {
				return err
			}
			defer sess.Close()
			stream, err := sess.AcceptStream()
			if err != nil {
				return err
			}
			defer stream.Close()
			id, err := rpc.ReadID(stream)
			if err != nil {
				return err
			} else if id != rpcGreet {
				return errors.New("unexpected RPC ID")
			}
			var name objString
			if err := rpc.ReadRequest(stream, &name); err != nil {
				return err
			}
			greeting := "Hello, " + name
			if err := rpc.WriteResponse(stream, &greeting); err != nil {
				return err
			}
			return nil
		}()
	}()

	// connect to peer
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sess, err := DialSession(conn, genesisID, UniqueID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	stream := sess.DialStream()
	defer stream.Close()

	name := objString("foo")
	var greeting objString
	if err := rpc.WriteRequest(stream, rpcGreet, &name); err != nil {
		t.Fatal(err)
	} else if err := rpc.ReadResponse(stream, &greeting); err != nil {
		t.Fatal(err)
	} else if greeting != "Hello, foo" {
		t.Fatal("unexpected greeting:", greeting)
	}
	if err := <-peerErr; err != nil {
		t.Fatal(err)
	}
}
