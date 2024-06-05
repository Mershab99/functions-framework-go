package knative

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/go-chi/chi/v5"
	"k8s.io/klog/v2"

	ofctx "github.com/Mershab99/functions-framework-go/context"
	"github.com/Mershab99/functions-framework-go/internal/functions"
	"github.com/Mershab99/functions-framework-go/plugin"
	"github.com/Mershab99/functions-framework-go/runtime"
)

const (
	functionStatusHeader = "X-OpenFunction-Status"
	crashStatus          = "crash"
	errorStatus          = "error"
	successStatus        = "success"
	defaultPattern       = "/"
)

type Runtime struct {
	port    string
	pattern string
	handler *chi.Mux
}

func NewKnativeRuntime(port string, pattern string) *Runtime {
	if pattern == "" {
		pattern = defaultPattern
	}
	return &Runtime{
		port:    port,
		pattern: pattern,
		handler: chi.NewRouter(),
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	klog.Infof("Knative Function serving http: listening on port %s", r.port)
	klog.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", r.port), r.handler))
	return nil
}

func (r *Runtime) RegisterOpenFunction(
	ctx ofctx.RuntimeContext,
	prePlugins []plugin.Plugin,
	postPlugins []plugin.Plugin,
	rf *functions.RegisteredFunction,
) error {
	// Initialize dapr client if FuncContext defined inputs or outputs
	if ctx.HasInputs() || ctx.HasOutputs() {
		ctx.InitDaprClientIfNil()
	}

	fn := func(w http.ResponseWriter, r *http.Request) {
		rm := runtime.NewRuntimeManager(ctx, prePlugins, postPlugins)
		// save the Vars into the context
		_ctx := ofctx.CtxWithVars(r.Context(), ofctx.URLParamsFromCtx(r.Context()))
		rm.FuncContext.SetNativeContext(_ctx)
		rm.FuncContext.SetSyncRequest(w, r.WithContext(_ctx))
		defer RecoverPanicHTTP(w, "Function panic")
		rm.FunctionRunWrapperWithHooks(rf.GetOpenFunctionFunction())

		switch rm.FuncOut.GetCode() {
		case ofctx.Success:
			w.Header().Set(functionStatusHeader, successStatus)
			w.WriteHeader(rm.FuncOut.GetCode())
			w.Write(rm.FuncOut.GetData())
			return
		case ofctx.InternalError:
			w.Header().Set(functionStatusHeader, errorStatus)
			w.WriteHeader(rm.FuncOut.GetCode())
			return
		default:
			return
		}
	}

	methods := rf.GetFunctionMethods()
	// Register the synchronous function (based on Knaitve runtime)
	if len(methods) > 0 {
		// add methods matcher if provided
		for _, method := range methods {
			r.handler.MethodFunc(method, rf.GetPath(), fn)
		}
	} else {
		r.handler.HandleFunc(rf.GetPath(), fn)
	}

	return nil
}

func (r *Runtime) RegisterHTTPFunction(
	ctx ofctx.RuntimeContext,
	prePlugins []plugin.Plugin,
	postPlugins []plugin.Plugin,
	rf *functions.RegisteredFunction,
) error {
	fn := func(w http.ResponseWriter, r *http.Request) {
		rm := runtime.NewRuntimeManager(ctx, prePlugins, postPlugins)
		// save the Vars into the context
		_ctx := ofctx.CtxWithVars(r.Context(), ofctx.URLParamsFromCtx(r.Context()))
		rm.FuncContext.SetNativeContext(_ctx)
		rm.FuncContext.SetSyncRequest(w, r.WithContext(_ctx))
		defer RecoverPanicHTTP(w, "Function panic")
		rm.FunctionRunWrapperWithHooks(rf.GetHTTPFunction())
	}

	methods := rf.GetFunctionMethods()
	if len(methods) > 0 {
		// add methods matcher if provided
		for _, method := range methods {
			r.handler.MethodFunc(method, rf.GetPath(), fn)
		}
	} else {
		r.handler.HandleFunc(rf.GetPath(), fn)
	}

	return nil
}

func (r *Runtime) RegisterCloudEventFunction(
	ctx context.Context,
	funcContext ofctx.RuntimeContext,
	prePlugins []plugin.Plugin,
	postPlugins []plugin.Plugin,
	rf *functions.RegisteredFunction,
) error {
	p, err := cloudevents.NewHTTP()
	if err != nil {
		klog.Errorf("failed to create protocol: %v\n", err)
		return err
	}

	handleFn, err := cloudevents.NewHTTPReceiveHandler(ctx, p, func(ctx context.Context, ce cloudevents.Event) error {
		rm := runtime.NewRuntimeManager(funcContext, prePlugins, postPlugins)
		// save the native ctx
		rm.FuncContext.SetNativeContext(ctx)
		rm.FuncContext.SetEvent("", &ce)
		rm.FunctionRunWrapperWithHooks(rf.GetCloudEventFunction())
		return rm.FuncContext.GetError()
	})

	if err != nil {
		klog.Errorf("failed to create handler: %v\n", err)
		return err
	}

	// function to extract Vars and add into ctx
	withVars := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ctx := ofctx.CtxWithVars(r.Context(), ofctx.URLParamsFromCtx(r.Context()))
			next.ServeHTTP(w, r.WithContext(_ctx))
		})
	}
	r.handler.Handle(rf.GetPath(), withVars(handleFn))
	return nil
}

func (r *Runtime) Name() ofctx.Runtime {
	return ofctx.Knative
}

func (r *Runtime) GetHandler() interface{} {
	return r.handler
}

func RecoverPanicHTTP(w http.ResponseWriter, msg string) {
	if r := recover(); r != nil {
		writeHTTPErrorResponse(w, http.StatusInternalServerError, crashStatus, fmt.Sprintf("%s: %v\n\n%s", msg, r, debug.Stack()))
	}
}

func writeHTTPErrorResponse(w http.ResponseWriter, statusCode int, status, msg string) {
	// Ensure logs end with a newline otherwise they are grouped incorrectly in SD.
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(os.Stderr, msg)

	w.Header().Set(functionStatusHeader, status)
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, msg)
}
