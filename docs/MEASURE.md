# MEASURE — every published number comes from a command here

## Test upstream (laptop)

```
~/Desktop/repos/2185Lab/bunny-kit/binaries/llama-server/b9553/llama-server \
  -m ~/.cache/bunny-network/models/gemma-4-E2B-it-Q4_K_M.gguf \
  --host 127.0.0.1 --port 18080 -np 2 -c 8192 --no-mmproj --metrics
```

Note: llama.cpp divides `-c` across `-np` slots; with `-c 8192 -np 2` the engine reports `n_ctx` 4096 **per slot**, and that is the effective context the gateway must enforce.

## Numbers (fill from ticket reports; date + command + result)

| Date | What | Command | Result |
|---|---|---|---|
