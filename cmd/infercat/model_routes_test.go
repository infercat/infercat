package main

import (
	"context"
	"github.com/infercat/infercat/internal/gateway"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestFriendRoutesMatchRouter(t *testing.T) {
	audio, err := upstream.OpenAudio(context.Background(), fakeEngine(t), "")
	if err != nil {
		t.Fatal(err)
	}
	images, err := upstream.OpenImages(context.Background(), fakeEngine(t), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, hasImages := range []bool{false, true} {
		for _, transcribe := range []bool{false, true} {
			for _, speech := range []bool{false, true} {
				cfg := gateway.Config{}
				if hasImages {
					cfg.Images = images
				}
				if transcribe {
					cfg.Transcribe = audio
				}
				if speech {
					cfg.Speech = audio
				}
				g := gateway.New(cfg, nil, nil, nil, nil)
				got := strings.Split(strings.ReplaceAll(friendRoutes(transcribe, speech, hasImages), " and ", ", "), ", ")
				sort.Strings(got)
				if want := g.EngineRoutes(); !reflect.DeepEqual(got, want) {
					t.Fatalf("banner %v router %v", got, want)
				}
				for _, path := range got {
					if word, class := usage.ModelEndpoint(path); class != "" && endpointWord(path) != word {
						t.Fatalf("status classifier drift: %s", path)
					}
				}
			}
		}
	}
	if endpointWord("/v1/images/generations") != "images" {
		t.Fatal("missing images label")
	}
	if endpointWord("/v1/responses") != "responses" {
		t.Fatal("missing status label")
	}
}
