package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infercat/infercat/internal/admin"
	"github.com/infercat/infercat/internal/keys"
	"github.com/infercat/infercat/internal/upstream"
	"github.com/infercat/infercat/internal/usage"
)

func TestAudioFlagsReloadAndStatus(t *testing.T) {
	dir, err := os.MkdirTemp("", "ic078-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var healthy atomic.Bool
	healthy.Store(true)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "offline", 503)
			return
		}
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	gw := newFakeGateway()
	plat := testPlatform(fakeAddr, nil)
	plat.startTunnel = func(context.Context, tunnelOptions) (tunnelServer, error) { return fakeTunnel{}, nil }
	var audio upstream.AudioEngine
	var images upstream.ImageEngine
	plat.newGateway = func(o gatewayOptions, _ upstream.Upstream, _ keys.Store, _ usage.Recorder, _ func(string, ...any)) (gatewayServer, error) {
		audio = o.Transcribe
		images = o.Images
		if images == nil || o.ImageModel != "image-model" {
			t.Error("image config did not reach gateway")
		}
		if o.SpeechVoices["zh"] != "zf_xiaoxiao" || o.Speech == nil || o.MaxTranscriptionSeconds != 45 || o.TranscribeModel != "asr-model" || o.SpeechModel != "tts-model" {
			t.Error("audio config did not reach gateway")
		}
		return gw, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errw lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--console", "off", "--data-dir", dir, "--upstream", engine.URL, "--upstream-transcribe", engine.URL, "--upstream-transcribe-key", "asr-key", "--upstream-speech", engine.URL, "--upstream-speech-key", "tts-key", "--max-transcription-seconds", "45", "--upstream-transcribe-model", "asr-model", "--upstream-speech-model", "tts-model", "--upstream-speech-voices", "zh=zf_xiaoxiao,default=af_heart", "--upstream-images", engine.URL, "--upstream-images-key", "image-key", "--upstream-images-model", "image-model"}, &out, &errw, nil, false, plat)
	}()
	select {
	case <-gw.serving:
	case <-time.After(5 * time.Second):
		t.Fatalf("serve not ready: %s", errw.String())
	}
	cfg, err := loadConfig(dir)
	if err != nil || cfg.UpstreamImages != engine.URL || cfg.UpstreamImagesKey != "image-key" || cfg.UpstreamImagesModel != "image-model" || cfg.UpstreamTranscribe != engine.URL || cfg.UpstreamSpeechKey != "tts-key" || cfg.MaxTranscriptionSeconds != 45 || cfg.UpstreamTranscribeModel != "asr-model" || cfg.UpstreamSpeechModel != "tts-model" || cfg.UpstreamSpeechVoices != "zh=zf_xiaoxiao,default=af_heart" {
		t.Fatalf("remembered config %+v %v", cfg, err)
	}
	fi, _ := os.Stat(configPath(dir))
	if fi.Mode().Perm() != 0600 {
		t.Fatal("bearer config permissions")
	}
	if !audio.Info().Health.OK {
		t.Fatal("initial audio probe failed")
	}
	healthy.Store(false)
	if err := admin.Reload(context.Background(), dir); err == nil {
		t.Fatal("reload hid failed probes")
	}
	if audio.Info().Health.OK || images.Info().Health.OK {
		t.Fatal("reload did not re-probe")
	}
	healthy.Store(true)
	if err := admin.Reload(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exit %d %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "/v1/audio/transcriptions") || !strings.Contains(out.String(), "/v1/audio/speech") {
		t.Fatal("banner omitted routes")
	}
	if strings.Contains(out.String()+errw.String(), "asr-key") || strings.Contains(out.String()+errw.String(), "tts-key") {
		t.Fatal("banner exposed engine keys")
	}
}
func TestAudioKeyFlags(t *testing.T) {
	dir := t.TempDir()
	plat := testPlatform(fakeAddr, nil)
	r := exec(t, plat, "keys", "add", "audio", "--daily-audio-seconds", "15", "--daily-speech-chars", "20", "--daily-images", "3", "--max-queued-images", "2", "--data-dir", dir)
	if r.code != 0 {
		t.Fatalf("add %d %s", r.code, r.err)
	}
	s, err := keys.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.Find(context.Background(), "audio")
	if err != nil || k.Limits.DailyAudioSeconds != 15 || k.Limits.DailySpeechChars != 20 || k.Limits.DailyImages != 3 || k.Limits.MaxQueuedImages != 2 {
		t.Fatal("limits flags not stored")
	}
	r = exec(t, plat, "keys", "list", "--data-dir", dir)
	if !strings.Contains(r.out, "AUDIO S/DAY") || !strings.Contains(r.out, "SPEECH CHARS/DAY") || !strings.Contains(r.out, "IMAGES/DAY") || !strings.Contains(r.out, "IMAGE QUEUE") {
		t.Fatal("keys list lacks audio limits")
	}
}
