#!/usr/bin/env bash
# Starts llama.cpp's llama-server for one GGUF model with the flags of the subtitle
# translation plan (phase F1 comparison), waits until the model is loaded and prints
# the cold load time to pass to `subtranslate-eval run -cold-load`.
#
# Usage:
#   scripts/subtranslate-llama-server.sh <model.gguf> [extra llama-server flags...]
#
# Environment (all optional):
#   LLAMA_SERVER   path to llama-server   (default: llama-server on PATH)
#   LLAMA_DEVICE   device to offload to   (default: unset = llama-server's choice;
#                  e.g. Vulkan1 for the dGPU on a Vulkan build, CUDA0 on a CUDA build;
#                  list them with: llama-server --list-devices)
#   LLAMA_HOST     listen address         (default: 127.0.0.1)
#   LLAMA_PORT     port                   (default: 8080)
#   LLAMA_NGL      layers on the GPU      (default: 99 = all)
#   LLAMA_CTX      total context          (default: 8192, 4K per slot with 2 slots, which
#                  fits a 4B Q4 model in 4 GB with the Vulkan backend; the plan's
#                  16384 runs out of VRAM there. TranslateGemma: 2K per slot, its
#                  trained context)
#   LLAMA_SLOTS    parallel slots         (default: 2)
#   LLAMA_LOG      log file               (default: $TMPDIR/subtranslate-llama-server.log)
#
# The server runs in the foreground; stop it with Ctrl+C. It listens on 127.0.0.1
# only. Speed numbers depend on the backend (Vulkan, CUDA, CPU): record it with
# `subtranslate-eval run -backend`.
set -euo pipefail

if [ "$#" -lt 1 ] || [ ! -f "$1" ]; then
    sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
fi
model="$1"
shift

server="${LLAMA_SERVER:-llama-server}"
host="${LLAMA_HOST:-127.0.0.1}"
port="${LLAMA_PORT:-8080}"
ngl="${LLAMA_NGL:-99}"
slots="${LLAMA_SLOTS:-2}"
log="${LLAMA_LOG:-${TMPDIR:-/tmp}/subtranslate-llama-server.log}"
name="$(basename "$model" .gguf)"
lower="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')"

ctx="${LLAMA_CTX:-8192}"
extra=()
case "$lower" in
*translategemma*)
    # Trained with a 2K context: 2K per slot.
    ctx="${LLAMA_CTX:-$((2048 * slots))}"
    ;;
*qwen3*instruct-2507*)
    # No thinking mode in Instruct-2507.
    ;;
*qwen3*)
    # Hybrid thinking models (Qwen3, Qwen3.5): thinking off on the server too; the
    # eval tool also sends chat_template_kwargs.enable_thinking=false. Some GGUFs'
    # templates always open <think> and ignore enable_thinking (seen with a
    # Qwen3.5-4B GGUF): a reasoning budget of 0 closes it at once.
    extra+=(--reasoning off --reasoning-budget 0)
    ;;
esac

flags=(-m "$model" --alias "$name"
    -ngl "$ngl" -c "$ctx" -np "$slots"
    -fa on --cache-type-k q8_0 --cache-type-v q8_0
    -b 2048 -ub 512 --jinja
    --temp 0.2 --top-p 0.9 --seed 42
    --host "$host" --port "$port" --no-webui --metrics)
if [ -n "${LLAMA_DEVICE:-}" ]; then
    flags+=(--device "$LLAMA_DEVICE")
fi

# Fail early on a llama-server build that doesn't know a flag.
help="$("$server" --help 2>&1 || true)"
for f in --flash-attn --cache-type-k --parallel --jinja --metrics --no-webui --alias; do
    if ! grep -q -- "$f" <<<"$help"; then
        echo "llama-server at $server doesn't support $f; update llama.cpp" >&2
        exit 1
    fi
done
if [ "${#extra[@]}" -gt 0 ] && ! grep -q -- "--reasoning-budget" <<<"$help"; then
    echo "note: this llama-server has no --reasoning/--reasoning-budget flags; relying on enable_thinking=false per request" >&2
    extra=()
fi

echo "starting $name (ctx $ctx, $slots slots, ngl $ngl${LLAMA_DEVICE:+, device $LLAMA_DEVICE}); log: $log" >&2
start=$(date +%s.%N)
"$server" "${flags[@]}" "${extra[@]}" "$@" >"$log" 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null' INT TERM EXIT

until curl -fs "http://$host:$port/health" >/dev/null 2>&1; do
    if ! kill -0 "$pid" 2>/dev/null; then
        echo "llama-server exited; last log lines:" >&2
        tail -n 20 "$log" >&2
        exit 1
    fi
    sleep 0.25
done
end=$(date +%s.%N)
load=$(awk -v a="$start" -v b="$end" 'BEGIN { printf "%.1f", b - a }')
echo "model loaded in ${load}s: http://$host:$port (pass -cold-load $load to subtranslate-eval run)" >&2
wait "$pid"
