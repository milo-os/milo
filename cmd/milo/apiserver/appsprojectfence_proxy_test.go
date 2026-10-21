package app

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Raw gRPC relay pauses the real outbound etcd transaction, after all Milo
// admission has completed. It changes no request/response and adds no server hook.
type fenceRawCodec struct{}

func (fenceRawCodec) Name() string                  { return "proto" }
func (fenceRawCodec) Marshal(v any) ([]byte, error) { return *v.(*[]byte), nil }
func (fenceRawCodec) Unmarshal(b []byte, v any) error {
	*v.(*[]byte) = append([]byte(nil), b...)
	return nil
}

type fencePause struct {
	key     string
	arrived chan struct{}
	resume  chan struct{}
}
type fenceProxy struct {
	mu    sync.Mutex
	pause *fencePause
}

func (p *fenceProxy) arm(key string) *fencePause {
	p.mu.Lock()
	defer p.mu.Unlock()
	pause := &fencePause{key: key, arrived: make(chan struct{}), resume: make(chan struct{})}
	p.pause = pause
	return pause
}
func (p *fenceProxy) before(ctx context.Context, method string, raw []byte) error {
	if method != "/etcdserverpb.KV/Txn" {
		return nil
	}
	var txn pb.TxnRequest
	if txn.Unmarshal(raw) != nil {
		return nil
	}
	p.mu.Lock()
	pause := p.pause
	if pause != nil {
		found := false
		for _, op := range txn.Success {
			if put := op.GetRequestPut(); put != nil && string(put.Key) == pause.key {
				found = true
			}
			if del := op.GetRequestDeleteRange(); del != nil && string(del.Key) == pause.key {
				found = true
			}
		}
		if !found {
			pause = nil
		} else {
			p.pause = nil
			close(pause.arrived)
		}
	}
	p.mu.Unlock()
	if pause != nil {
		select {
		case <-pause.resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func startFenceProxy(t *testing.T, endpoint string) (string, *fenceProxy) {
	t.Helper()
	upstream, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxy := &fenceProxy{}
	server := grpc.NewServer(grpc.ForceServerCodec(fenceRawCodec{}), grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		method, _ := grpc.MethodFromServerStream(stream)
		ctx, cancel := context.WithCancel(stream.Context())
		defer cancel()
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		remote, err := upstream.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, method, grpc.ForceCodec(fenceRawCodec{}))
		if err != nil {
			return err
		}
		sent := make(chan error, 1)
		go func() {
			for {
				var raw []byte
				if err := stream.RecvMsg(&raw); err != nil {
					if err == io.EOF {
						sent <- remote.CloseSend()
					} else {
						sent <- err
					}
					return
				}
				if err := proxy.before(ctx, method, raw); err != nil {
					sent <- err
					cancel()
					return
				}
				if err := remote.SendMsg(&raw); err != nil {
					sent <- err
					return
				}
			}
		}()
		for {
			var raw []byte
			err := remote.RecvMsg(&raw)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			if err = stream.SendMsg(&raw); err != nil {
				return err
			}
		}
	}))
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); upstream.Close(); listener.Close() })
	return "http://" + listener.Addr().String(), proxy
}
