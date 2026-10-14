FROM ollama/ollama:0.40.2

ENV OLLAMA_HOST=0.0.0.0:11434 \
    OLLAMA_KEEP_ALIVE=-1 \
    OLLAMA_CONTEXT_LENGTH=2048 \
    OLLAMA_NUM_PARALLEL=1 \
    MODEL=gemma3:1b

# Bake the model into the image so a restart never re-downloads it (design §5).
RUN (ollama serve >/dev/null 2>&1 &) \
    && until ollama list >/dev/null 2>&1; do sleep 1; done \
    && ollama pull "$MODEL"

COPY start.sh /start.sh
RUN chmod +x /start.sh
EXPOSE 11434
ENTRYPOINT ["/start.sh"]
