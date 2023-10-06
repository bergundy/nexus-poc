package main

import (
	"context"
	"crypto/sha1"
	"fmt"
	"log"
	"strings"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type MyInput struct {
	CellID string
	Stuff  int64
}
type MyOutput struct {
}
type MyIntermediateOutput struct{}
type MyMappedOutput struct{}

func MyHandlerWorkflow(ctx workflow.Context, input MyInput) (MyOutput, error) {
	return MyOutput{}, nil
}

func MyCallerWorkflow(ctx workflow.Context) (MyOutput, error) {
	handle, _ := workflow.StartOperation(ctx, startWorkflowOp, MyInput{})
	_ = handle.WaitStarted(ctx)
	_, _ = workflow.StartOperation(ctx, startWorkflowWithMapperOp, MyInput{})
	_, _ = workflow.StartOperation(ctx, queryOp, MyInput{})
	_, _ = workflow.StartNamedOperation[MyInput, MyOutput](ctx, "some-op", MyInput{})
	voidHandle, _ := workflow.StartVoidOperation(ctx, signalOp, MyInput{})
	_ = voidHandle.WaitStarted(ctx)
	_ = voidHandle.WaitCompleted(ctx)
	_, _ = workflow.StartNamedVoidOperation(ctx, "some-op", MyInput{})
	return handle.GetResult(ctx)
}

var startWorkflowSimple = temporalnexus.NewWorkflowRunOperation("provision-cell-simple", temporalnexus.WorkflowRunOptions[MyInput, MyOutput]{
	Workflow: MyHandlerWorkflow,
	GetOptions: func(ctx context.Context, input MyInput) (client.StartWorkflowOptions, error) {
		return client.StartWorkflowOptions{
			ID: constructID(ctx, "provision-cell", input.CellID),
		}, nil
	},
})

var startWorkflowOp = temporalnexus.NewWorkflowRunOperation("provision-cell", temporalnexus.WorkflowRunOptions[MyInput, MyOutput]{
	Start: func(ctx context.Context, c client.Client, input MyInput) (temporalnexus.WorkflowHandle[MyOutput], error) {
		return temporalnexus.StartWorkflow(ctx, c, client.StartWorkflowOptions{
			ID: constructID(ctx, "provision-cell", input.CellID),
		}, MyHandlerWorkflow, input)
	},
})

var queryOp = temporalnexus.NewSyncOperation("get-cell-status", func(ctx context.Context, c client.Client, input MyInput) (MyOutput, error) {
	payload, _ := c.QueryWorkflow(ctx, constructID(ctx, "provision-cell", input.CellID), "", "get-cell-status")
	var output MyOutput
	return output, payload.Get(&output)
})

var signalOp = temporalnexus.NewSyncOperation("set-cell-status", func(ctx context.Context, c client.Client, input MyInput) (nexus.NoResult, error) {
	return nil, c.SignalWorkflow(ctx, constructID(ctx, "provision-cell", input.CellID), "", "set-cell-status", input)
})

var startWorkflowWithMapperOp = nexus.WithMapper[MyInput, MyOutput, MyOutput, MyMappedOutput](
	startWorkflowOp,
	func(ctx context.Context, mo MyOutput, uoe *nexus.UnsuccessfulOperationError) (MyMappedOutput, *nexus.UnsuccessfulOperationError, error) {
		return MyMappedOutput{}, nil, nil
	},
)

func main() {
	c, err := client.Dial(client.Options{
		HostPort:  "localhost:7233",
		Namespace: "default",
	})
	if err != nil {
		log.Panic(err)
	}

	w := worker.New(c, "my-task-queue", worker.Options{})
	w.RegisterOperation(startWorkflowSimple)
	w.RegisterOperation(startWorkflowWithMapperOp)
	w.RegisterOperation(queryOp)
	w.RegisterOperation(signalOp)
	w.RegisterWorkflow(MyCallerWorkflow)
	w.RegisterWorkflow(MyHandlerWorkflow)
	w.Start()
	defer w.Stop()

	c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		TaskQueue: "my-task-queue",
	}, MyCallerWorkflow)

}

type tenantIDKey struct{}

func constructID(ctx context.Context, operation string, parts ...string) string {
	tenantID := ctx.Value(tenantIDKey{}).(string)

	return operation + "-" + tenantID + "-" + strings.Join(parts, "-")
}

func hashSource(s string) string {
	sum := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}

// type operationKey struct{}

// // w := worker.New(c, "my-task-queue", worker.Options{Interceptors: []interceptor.WorkerInterceptor{&OperationAuthorizationInterceptor{}, &ReencryptionInterceptor{}}})
// type OperationAuthorizationInterceptor struct {
// 	interceptor.WorkerInterceptorBase
// 	interceptor.OperationInboundInterceptorBase
// 	interceptor.OperationOutboundInterceptorBase
// }

// func (i *OperationAuthorizationInterceptor) getTenantID(h http.Header) (string, error) {
// 	source := h.Get("Temporal-Source-Namespace")
// 	if source == "" {
// 		return "", operation.NewUnauthorizedError("unauthorized access")
// 	}
// 	return hashSource(source), nil
// }

// func (i *OperationAuthorizationInterceptor) StartOperation(ctx context.Context, request *nexus.StartOperationRequest) (nexus.OperationResponse, error) {
// 	tenantID, err := i.getTenantID(request.HTTPRequest.Header)
// 	if err != nil {
// 		return nil, err
// 	}
// 	ctx = context.WithValue(ctx, tenantIDKey{}, tenantID)
// 	ctx = context.WithValue(ctx, operationKey{}, request.Operation)
// 	return i.OperationInboundInterceptorBase.Next.StartOperation(ctx, request)
// }

// func (i *OperationAuthorizationInterceptor) CancelOperation(ctx context.Context, request *nexus.CancelOperationRequest) error {
// 	tenantID, err := i.getTenantID(request.HTTPRequest.Header)
// 	if err != nil {
// 		return err
// 	}

// 	if !strings.HasPrefix(request.OperationID, fmt.Sprintf("%s:%s:", request.Operation, tenantID)) {
// 		return operation.NewUnauthorizedError("unauthorized access")
// 	}
// 	return i.OperationInboundInterceptorBase.Next.CancelOperation(ctx, request)
// }

// func (i *OperationAuthorizationInterceptor) ExecuteOperation(ctx context.Context, input *interceptor.ClientExecuteWorkflowInput) (client.WorkflowRun, error) {
// 	operation := ctx.Value(operationKey{}).(string)

// 	if !strings.HasPrefix(input.Options.ID, constructID(ctx, operation)) {
// 		return nil, errors.New("Workflow ID does not match expected format")
// 	}

// 	return i.OperationOutboundInterceptorBase.Next.ExecuteOperation(ctx, input)
// }

// type encryptionKeyKey struct{}

// type ReencryptionInterceptor struct {
// 	interceptor.WorkerInterceptorBase
// 	interceptor.OperationInboundInterceptorBase
// 	interceptor.OperationOutboundInterceptorBase
// }

// func (i *ReencryptionInterceptor) StartOperation(ctx context.Context, request *nexus.StartOperationRequest) (nexus.OperationResponse, error) {
// 	responseEncryptionKey := request.HTTPRequest.Header.Get("Response-Encryption-Key")
// 	if responseEncryptionKey != "" {
// 		ctx = context.WithValue(ctx, encryptionKeyKey{}, responseEncryptionKey)
// 	}

// 	return i.OperationInboundInterceptorBase.Next.StartOperation(ctx, request)
// }

// func (i *ReencryptionInterceptor) ExecuteOperation(ctx context.Context, input *interceptor.ClientExecuteWorkflowInput) (client.WorkflowRun, error) {
// 	if _, ok := ctx.Value(encryptionKeyKey{}).(string); ok {
// 		opts := *input.Options
// 		input.Options = &opts
// 		// TODO: inject the key into the callback context.
// 		return i.OperationOutboundInterceptorBase.Next.ExecuteOperation(ctx, input)
// 	}

// 	return i.OperationOutboundInterceptorBase.Next.ExecuteOperation(ctx, input)
// }

// func (i *ReencryptionInterceptor) MapCompletion(ctx context.Context, request *MapCompletionRequest) (nexus.OperationCompletion, error) {
// 	// converter.NewEnc
// }