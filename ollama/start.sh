#!/bin/sh
# Start Ollama, then load the model into RAM so the first real request is not a cold start.
set -e
ollama serve &
pid=$!
until ollama list >/dev/null 2>&1; do sleep 1; done
ollama run "$MODEL" "Say ready." >/dev/null 2>&1 || echo "warm-up failed; model will load on first request"
echo "ollama ready with $MODEL"
wait "$pid"
