package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	hclog "github.com/hashicorp/go-hclog"
	processplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

const rpcName = "terragraph.plugin.v1.Service"

// Handshake prevents accidental execution as a plugin; package hashes remain the trust check.
var Handshake = processplugin.HandshakeConfig{ProtocolVersion: ProtocolVersion, MagicCookieKey: "TERRAGRAPH_PLUGIN", MagicCookieValue: "terragraph-plugin-v1"}

type codec struct{}

func (codec) Name() string                  { return "terragraph-plugin-v1" }
func (codec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }
func (codec) Unmarshal(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid trailing protocol content")
	}
	return nil
}

func init() { encoding.RegisterCodec(codec{}) }

// RPC implements one immutable descriptor and typed requests without importing the host's internal packages.
type service interface {
	Describe(context.Context) (Descriptor, error)
	Invoke(context.Context, Request) (Response, error)
}

// RPC includes a separate progress stream so Invoke never buffers logs until completion.
type RPC interface {
	service
	OpenLogs(context.Context) (LogStream, error)
}

type executable struct {
	processplugin.NetRPCUnsupportedPlugin
	descriptor Descriptor
	handler    Handler
	logs       *logHub
}

func (e *executable) GRPCServer(_ *processplugin.GRPCBroker, s *grpc.Server) error {
	e.logs = &logHub{queue: make(chan LogRecord, LogQueueSize)}
	s.RegisterService(&grpc.ServiceDesc{ServiceName: rpcName, HandlerType: (*service)(nil), Methods: []grpc.MethodDesc{
		{MethodName: "Describe", Handler: describeRPC},
		{MethodName: "Invoke", Handler: invokeRPC},
	}, Streams: []grpc.StreamDesc{{StreamName: "Logs", Handler: serveLogs, ServerStreams: true}, {StreamName: "InvokePlan", Handler: servePlan, ClientStreams: true}}}, e)
	return nil
}
func (e *executable) GRPCClient(_ context.Context, _ *processplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &client{c: c}, nil
}
func (e *executable) Describe(context.Context) (Descriptor, error) { return e.descriptor, nil }
func (e *executable) Invoke(ctx context.Context, r Request) (result Response, err error) {
	defer func() {
		entry := LogRecord{CallID: r.ID, Complete: true, Dropped: e.logs.dropped.Swap(0)}
		select {
		case e.logs.queue <- entry:
		default:
			result.LogsIncomplete = true
			e.logs.dropped.Add(entry.Dropped)
		}
	}()
	defer func() {
		if recover() != nil {
			result = Response{Fault: &Fault{Code: "plugin_panic", Fatal: true}}
			err = nil
		}
	}()
	if e.handler == nil {
		return Response{Fault: &Fault{Code: "handler_missing", Fatal: true}}, nil
	}
	ctx = context.WithValue(ctx, loggerKey{}, slog.New(&logHandler{hub: e.logs, callID: r.ID, level: slog.Level(r.LogLevel)}))
	result, err = e.handler(ctx, r)
	if err != nil {
		return Response{Fault: &Fault{Code: "handler_failed"}}, nil
	}
	return result, nil
}

func describeRPC(s any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var input struct{}
	if err := decode(&input); err != nil {
		return nil, err
	}
	h := func(ctx context.Context, _ any) (any, error) { return s.(service).Describe(ctx) }
	if interceptor == nil {
		return h(ctx, &input)
	}
	return interceptor(ctx, &input, &grpc.UnaryServerInfo{Server: s, FullMethod: "/" + rpcName + "/Describe"}, h)
}
func invokeRPC(s any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var input Request
	if err := decode(&input); err != nil {
		return nil, err
	}
	h := func(ctx context.Context, v any) (any, error) { return s.(service).Invoke(ctx, *v.(*Request)) }
	if interceptor == nil {
		return h(ctx, &input)
	}
	return interceptor(ctx, &input, &grpc.UnaryServerInfo{Server: s, FullMethod: "/" + rpcName + "/Invoke"}, h)
}

type client struct{ c *grpc.ClientConn }

func (c *client) Describe(ctx context.Context) (Descriptor, error) {
	var d Descriptor
	err := c.c.Invoke(ctx, "/"+rpcName+"/Describe", &struct{}{}, &d, grpc.ForceCodec(codec{}), grpc.MaxCallRecvMsgSize(MaxMessageSize))
	return d, err
}
func (c *client) Invoke(ctx context.Context, r Request) (Response, error) {
	if len(r.Event.Plan) > 0 {
		return c.invokePlanStream(ctx, r)
	}
	var result Response
	err := c.c.Invoke(ctx, "/"+rpcName+"/Invoke", &r, &result, grpc.ForceCodec(codec{}), grpc.MaxCallRecvMsgSize(MaxMessageSize), grpc.MaxCallSendMsgSize(MaxMessageSize))
	return result, err
}

func ClientPlugins() processplugin.PluginSet {
	return processplugin.PluginSet{"terragraph": &executable{}}
}

// Serve isolates handler panics and suppresses raw logs that could include credentials.
func Serve(descriptor Descriptor, handler Handler) {
	processplugin.Serve(&processplugin.ServeConfig{HandshakeConfig: Handshake, Plugins: processplugin.PluginSet{"terragraph": &executable{descriptor: descriptor, handler: handler}}, GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
		return grpc.NewServer(append(opts, grpc.MaxRecvMsgSize(MaxMessageSize), grpc.MaxSendMsgSize(MaxMessageSize))...)
	}, Logger: hclog.New(&hclog.LoggerOptions{Output: io.Discard})})
}

func serveLogs(server any, stream grpc.ServerStream) error {
	var request struct{}
	if err := stream.RecvMsg(&request); err != nil {
		return err
	}
	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	hub := server.(*executable).logs
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case entry := <-hub.queue:
			entry.Dropped += hub.dropped.Swap(0)
			if err := stream.SendMsg(&entry); err != nil {
				return err
			}
		}
	}
}

type logStream struct{ grpc.ClientStream }

func (s *logStream) Recv() (LogRecord, error) {
	var entry LogRecord
	err := s.RecvMsg(&entry)
	return entry, err
}
func (c *client) OpenLogs(ctx context.Context) (LogStream, error) {
	stream, err := c.c.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/"+rpcName+"/Logs", grpc.ForceCodec(codec{}), grpc.MaxCallRecvMsgSize(256<<10))
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(&struct{}{}); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	if _, err := stream.Header(); err != nil {
		return nil, err
	}
	return &logStream{stream}, nil
}
