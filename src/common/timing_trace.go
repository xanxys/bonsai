package main

import (
	"./api"
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/net/context"
	"log"
	"math/rand"
	"net/http"
	"time"
)

type TraceContext struct {
	trace *api.TimingTrace
	// Top-level if parent is nil.
	parent *api.TimingTrace
}

func TraceStart(ctx context.Context, name string) context.Context {
	trace, ok := ctx.Value("trace").(*TraceContext)
	if !ok {
		// No existing trace: top level trace
		trace := InitTrace(name)
		return context.WithValue(ctx, "trace", &TraceContext{
			trace: trace,
		})
	} else {
		// Existing trace: sub-trace
		cTrace := InitTrace(name)
		return context.WithValue(ctx, "trace", &TraceContext{
			trace:  cTrace,
			parent: trace.trace,
		})
	}
}

func TraceEnd(ctx context.Context, cred *ServerCred) {
	traceCtx, ok := ctx.Value("trace").(*TraceContext)
	if ok {
		FinishTrace(traceCtx.trace, traceCtx.parent)
		if traceCtx.parent == nil {
			go PostNewTrace(traceCtx.trace, cred)
		}
	} else {
		log.Printf("ERROR: Calling TraceEnd for non-TraceStart-ed context")
	}
}

func GetCurrentTrace(ctx context.Context) *api.TimingTrace {
	trace, ok := ctx.Value("trace").(*TraceContext)
	if ok {
		return trace.trace
	} else {
		log.Printf("FATAL: GetCurrentTrace for non-TraceStarted context")
		return nil
	}
}

func InitTrace(name string) *api.TimingTrace {
	return &api.TimingTrace{
		Name:  name,
		Start: time.Now().UnixNano(),
	}
}

func FinishTrace(child, parent *api.TimingTrace) {
	if child.End == 0 {
		child.End = time.Now().UnixNano()
	}
	if parent != nil {
		parent.Children = append(parent.Children, child)
	}
}

// Do best to post a new trace to cloud trace, but does not guarantee its success.
func PostNewTrace(trace *api.TimingTrace, cred *ServerCred) {
	ctx := context.Background()

	ctQ := ConvertToCloudTrace(trace)
	ctQJson, _ := json.Marshal(ctQ)
	httpQ, err := http.NewRequest(
		"PATCH",
		fmt.Sprintf("https://cloudtrace.googleapis.com/v1/projects/%s/traces", ProjectId),
		bytes.NewReader(ctQJson))
	httpQ.Header.Add("Content-Type", "application/json")
	if err != nil {
		log.Printf("Failed to craft http request %#v", err)
		return
	}

	httpClient := cred.AuthRawHttp(ctx)
	httpS, err := httpClient.Do(httpQ)
	if err != nil {
		log.Printf("Posting trace failed with error %#v request: %#v response: %#v", err, httpQ, httpS)
		return
	}
	if httpS.StatusCode != 200 {
		log.Printf("Trace patch request returned with non-200: %s response: %#v", string(ctQJson[:]), httpS)
	}
}

func ConvertToCloudTrace(trace *api.TimingTrace) *CTPatchRequest {
	tf := &TraceFlattener{}
	tf.Flatten("", trace)
	ctTrace := &CTTrace{
		ProjectId: ProjectId,
		TraceId:   random128bitHex(),
		Spans:     tf.spans,
	}
	return &CTPatchRequest{
		Traces: []*CTTrace{ctTrace},
	}
}

type TraceFlattener struct {
	spans []*CTSpan
}

func (tf *TraceFlattener) Flatten(parentSpanId string, tr *api.TimingTrace) {
	// Although undocumented, spanId must be uint64 >= 1.
	// (otherwise it fails with "INVALID_ARGUMENT" error)
	spanId := fmt.Sprintf("%d", len(tf.spans)+1)
	span := &CTSpan{
		SpanId:       spanId,
		Name:         tr.Name,
		StartTime:    time.Unix(0, tr.Start).Format(time.RFC3339Nano),
		EndTime:      time.Unix(0, tr.End).Format(time.RFC3339Nano),
		ParentSpanId: parentSpanId,
		Labels:       make(map[string]string),
	}
	tf.spans = append(tf.spans, span)
	for _, childSpan := range tr.Children {
		tf.Flatten(spanId, childSpan)
	}
}

func random128bitHex() string {
	return fmt.Sprintf("%08x%08x%08x%08x", rand.Uint32(), rand.Uint32(), rand.Uint32(), rand.Uint32())
}

type CTPatchRequest struct {
	Traces []*CTTrace `json:"traces"`
}

// See https://cloud.google.com/trace/api/reference/rest/v1/projects.traces
type CTTrace struct {
	ProjectId string    `json:"projectId"`
	TraceId   string    `json:"traceId"`
	Spans     []*CTSpan `json:"spans"`
}

type CTSpan struct {
	SpanId string `json:"spanId"`
	// "SPAN_KIND_UNSPECIFIED" "RPC_SERVER" "RPC_CLIENT"
	Kind         string            `json:"kind,omitempty"`
	Name         string            `json:"name"`
	StartTime    string            `json:"startTime"`
	EndTime      string            `json:"endTime"`
	ParentSpanId string            `json:"parentSpanId,omitempty"`
	Labels       map[string]string `json:"labels"`
}
