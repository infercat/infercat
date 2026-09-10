//go:build sherpa && cgo && (darwin || linux)

// infercat-speech is built separately from the host with the pinned sherpa C API.
package main

/*
#cgo LDFLAGS: -lsherpa-onnx-c-api
#include "bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/cgo"
	"syscall"
	"time"
	"unsafe"

	"github.com/infercat/infercat/internal/speech"
)

var version = "snapshot"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	root := flag.String("model-dir", "", "verified Kokoro v1.1-zh bundle directory")
	listen := flag.String("listen", "127.0.0.1:8082", "loopback listen address")
	show := flag.Bool("version", false, "print helper and sherpa versions")
	flag.Parse()
	if *show {
		fmt.Printf("infercat-speech %s; sherpa-onnx %s\n", version, C.GoString(C.speech_version()))
		return nil
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || *root == "" || flag.NArg() != 0 {
		return errors.New("model-dir and a literal loopback listen address are required")
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	dir := C.CString(*root)
	tts := C.speech_create(dir)
	C.free(unsafe.Pointer(dir))
	if tts == nil {
		return errors.New("cannot load pinned Kokoro bundle")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	generate := func(ctx context.Context, input string, voice int, speed float32, emit func([]float32) error) error {
		var failure error
		h := cgo.NewHandle(func(samples []float32) bool {
			if failure = ctx.Err(); failure == nil {
				failure = emit(samples)
			}
			return failure == nil
		})
		defer h.Delete()
		text := C.CString(input)
		defer C.free(unsafe.Pointer(text))
		ok := C.speech_generate(tts, text, C.int(voice), C.float(speed), C.uintptr_t(h))
		if failure != nil {
			return failure
		}
		if ok == 0 {
			return errors.New("native synthesis failed")
		}
		return nil
	}
	srv := &http.Server{Handler: speech.New(generate), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	finished := make(chan error, 1)
	go func() { finished <- srv.Serve(listener) }()
	select {
	case err = <-finished:
		stop()
	case <-ctx.Done():
	}
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if shutdown := srv.Shutdown(deadline); shutdown != nil {
		// A native batch may still be running. Exit without freeing its model.
		return shutdown
	}
	C.speech_destroy(tts)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

//export speech_chunk
func speech_chunk(data *C.float, count C.int, handle C.uintptr_t) C.int {
	if count < 0 || count > 120*speech.SampleRate {
		return 0
	}
	if cgo.Handle(handle).Value().(func([]float32) bool)(unsafe.Slice((*float32)(unsafe.Pointer(data)), int(count))) {
		return 1
	}
	return 0
}
