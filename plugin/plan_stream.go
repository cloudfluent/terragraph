package plugin

import (
	"context"
	"fmt"
	"io"

	"google.golang.org/grpc"
)

const MaxPlanSize = 64 << 20
const planChunkSize = 1 << 20

type planChunk struct {
	Data []byte `json:"data"`
}

// invokePlanStream bounds each transport frame while keeping large native plan documents out of the ordinary request budget.
func (c *client) invokePlanStream(ctx context.Context, r Request) (Response, error) {
	stream, err := c.c.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, "/"+rpcName+"/InvokePlan", grpc.ForceCodec(codec{}), grpc.MaxCallRecvMsgSize(MaxMessageSize), grpc.MaxCallSendMsgSize(MaxMessageSize))
	if err != nil {
		return Response{}, err
	}
	plan := r.Event.Plan
	r.Event.Plan = nil
	if err := stream.SendMsg(&r); err != nil {
		return Response{}, err
	}
	for len(plan) > 0 {
		n := min(len(plan), planChunkSize)
		if err := stream.SendMsg(&planChunk{Data: plan[:n]}); err != nil {
			return Response{}, err
		}
		plan = plan[n:]
	}
	if err := stream.CloseSend(); err != nil {
		return Response{}, err
	}
	var response Response
	err = stream.RecvMsg(&response)
	return response, err
}

func servePlan(server any, stream grpc.ServerStream) error {
	var request Request
	if err := stream.RecvMsg(&request); err != nil {
		return err
	}
	if len(request.Event.Plan) > 0 {
		return fmt.Errorf("plan must use bounded chunks")
	}
	for {
		var chunk planChunk
		err := stream.RecvMsg(&chunk)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if len(chunk.Data) == 0 || len(chunk.Data) > planChunkSize || len(request.Event.Plan)+len(chunk.Data) > MaxPlanSize {
			return fmt.Errorf("plan exceeds transfer limit")
		}
		request.Event.Plan = append(request.Event.Plan, chunk.Data...)
	}
	response, err := server.(service).Invoke(stream.Context(), request)
	if err != nil {
		return err
	}
	return stream.SendMsg(&response)
}
