//go:build sherpa && cgo && (darwin || linux)

#include "bridge.h"
#include <sherpa-onnx/c-api/c-api.h>
#include <stdio.h>
#include <stdlib.h>

extern int speech_chunk(float *, int, uintptr_t);
const char *speech_version(void) { return SherpaOnnxGetVersionStr(); }

void *speech_create(const char *root) {
  char model[4096], voices[4096], tokens[4096], data[4096], lexicon[8192];
  if (snprintf(model, sizeof(model), "%s/model.onnx", root) >= sizeof(model) ||
      snprintf(voices, sizeof(voices), "%s/voices.bin", root) >= sizeof(voices) ||
      snprintf(tokens, sizeof(tokens), "%s/tokens.txt", root) >= sizeof(tokens) ||
      snprintf(data, sizeof(data), "%s/espeak-ng-data", root) >= sizeof(data) ||
      snprintf(lexicon, sizeof(lexicon), "%s/lexicon-us-en.txt,%s/lexicon-zh.txt", root, root) >= sizeof(lexicon))
    return NULL;
  SherpaOnnxOfflineTtsConfig config = {0};
  config.model.kokoro.model = model;
  config.model.kokoro.voices = voices;
  config.model.kokoro.tokens = tokens;
  config.model.kokoro.data_dir = data;
  config.model.kokoro.lexicon = lexicon;
  config.model.kokoro.length_scale = 1;
  config.model.num_threads = 2;
  config.model.provider = "cpu";
  config.max_num_sentences = 1;
  config.silence_scale = .2;
  const SherpaOnnxOfflineTts *tts = SherpaOnnxCreateOfflineTts(&config);
  if (tts && (SherpaOnnxOfflineTtsSampleRate(tts) != 24000 || SherpaOnnxOfflineTtsNumSpeakers(tts) != 103)) {
    SherpaOnnxDestroyOfflineTts(tts);
    return NULL;
  }
  return (void *)tts;
}

void speech_destroy(void *tts) { SherpaOnnxDestroyOfflineTts(tts); }

struct stream { uintptr_t handle; int stopped; };

static int32_t chunk(const float *samples, int32_t n, float progress, void *arg) {
  (void)progress;
  struct stream *s = arg;
  if (!speech_chunk((float *)samples, n, s->handle)) s->stopped = 1;
  return !s->stopped;
}

int speech_generate(void *tts, const char *text, int voice, float speed, uintptr_t handle) {
  SherpaOnnxGenerationConfig config = {0};
  config.sid = voice;
  config.speed = speed;
  config.silence_scale = .2;
  struct stream s = {handle, 0};
  const SherpaOnnxGeneratedAudio *audio = SherpaOnnxOfflineTtsGenerateWithConfig(tts, text, &config, chunk, &s);
  int ok = audio && audio->n > 0 && !s.stopped;
  if (audio) SherpaOnnxDestroyOfflineTtsGeneratedAudio(audio);
  return ok;
}
