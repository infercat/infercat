#include <stdint.h>
const char *speech_version(void);
void *speech_create(const char *root);
void speech_destroy(void *tts);
int speech_generate(void *tts, const char *text, int voice, float speed, uintptr_t handle);
