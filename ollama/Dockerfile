FROM ollama/ollama:0.40.2

ENV OLLAMA_HOST=0.0.0.0:11434 \
    OLLAMA_KEEP_ALIVE=-1 \
    OLLAMA_CONTEXT_LENGTH=2048 \
    OLLAMA_NUM_PARALLEL=1 \
    OLLAMA_MAX_LOADED_MODELS=1 \
    MODEL=gemma3:4b

# The container sees the whole host (60+ GB, 16 CPUs), not the 2c-4g plan, so llama.cpp sized itself
# for a big machine and Render killed it for using over 4 GB. These are passed to llama-server:
# no prompt cache (it could grow to 8 GB), no repacked second copy of the weights (~1.4 GB), and
# 2 threads to match the plan's 2 CPUs instead of 8. Checked locally that all three take effect.
ENV LLAMA_ARG_CACHE_RAM=0 \
    LLAMA_ARG_NO_REPACK=1 \
    LLAMA_ARG_THREADS=2

# Bake the model into the image so a restart never re-downloads it (design §5).
RUN (ollama serve >/dev/null 2>&1 &) \
    && until ollama list >/dev/null 2>&1; do sleep 1; done \
    && ollama pull "$MODEL"

COPY start.sh /start.sh
RUN chmod +x /start.sh
EXPOSE 11434
ENTRYPOINT ["/start.sh"]
