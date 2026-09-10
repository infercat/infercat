# Responses fixtures

The request and event JSON files are verbatim sanitized 133 captures from Codex
0.154.0. Both requests advertise hosted web_search, so they are refusal fixtures.
Tests remove that hosted tool to form projections **derived from the 133 capture**.
The captured events came from the 133 synthetic reference, not a live engine.

Function-call streams in responses_test.go are **constructed** from the official
[function calling streaming contract](https://developers.openai.com/api/docs/guides/function-calling#streaming).
They are not captured 133 tool calls (133 captured none). Omission probes mirror
Codex 0.154.0's codex-api/src/sse/responses.rs: output_item.done supplies history;
response.completed marks successful completion.
