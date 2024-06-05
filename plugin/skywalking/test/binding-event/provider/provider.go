package main

import (
	"context"
	"time"

	"k8s.io/klog/v2"

	ofctx "github.com/Mershab99/functions-framework-go/context"
	"github.com/Mershab99/functions-framework-go/framework"
	"github.com/Mershab99/functions-framework-go/plugin"
	"github.com/Mershab99/functions-framework-go/plugin/skywalking"
)

func bindingsFunction(ofCtx ofctx.Context, in []byte) (ofctx.Out, error) {

	_, err := ofCtx.Send("sample-topic", []byte(time.Now().String()))
	if err != nil {
		klog.Error(err)
		return ofCtx.ReturnOnInternalError().WithData([]byte(err.Error())), err
	}

	return ofCtx.ReturnOnSuccess().WithData([]byte("hello there")), nil
}

func main() {
	ctx := context.Background()
	fwk, err := framework.NewFramework()
	if err != nil {
		klog.Fatal(err)
	}
	fwk.RegisterPlugins(map[string]plugin.Plugin{
		"skywalking": &skywalking.PluginSkywalking{},
	})

	err = fwk.Register(ctx, bindingsFunction)
	if err != nil {
		klog.Fatal(err)
	}

	err = fwk.Start(ctx)
	if err != nil {
		klog.Fatal(err)
	}
}
