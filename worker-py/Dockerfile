# ---- Base Image ----
# We use python:3.12-slim, NOT nvidia/cuda. Why?
#
# When you pip-install PyTorch with a CUDA index URL, the wheel bundles
# all the CUDA runtime libraries it needs (libcudart, libcublas, etc.)
# inside itself. So the container doesn't need CUDA pre-installed.
#
# The only thing needed on the HOST side is:
#   1. NVIDIA GPU drivers (you have these — you've done inference before)
#   2. NVIDIA Container Toolkit (lets Docker pass the GPU into containers)
#
# This keeps the image much smaller than nvidia/cuda base (~1GB vs ~4GB).
FROM python:3.12-slim

# ---- System Dependencies ----
# psycopg2-binary needs libpq under the hood. gcc + libpq-dev let it
# compile its C extension if a pre-built wheel isn't available.
# We clean up apt cache in the same RUN layer to keep the image small.
RUN apt-get update && \
    apt-get install -y --no-install-recommends gcc libpq-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# ---- Python Dependencies (two-step install) ----
# Step 1: Install PyTorch with CUDA 12.8 from PyTorch's own wheel index.
#
# Why a separate step? The --index-url flag replaces PyPI entirely for
# that command. If we mixed it with other packages, pip would try to
# find sentence-transformers on PyTorch's index (it's not there) and fail.
#
# Why cu128? Your RTX 5070 is Blackwell architecture — it needs CUDA 12.8+.
# The "cu128" wheels bundle CUDA 12.8 runtime inside the torch package.
#
# --no-cache-dir: Don't store pip's download cache in the image. Saves ~2GB
# since PyTorch wheels are huge.
RUN pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cu128

# Step 2: Install everything else from normal PyPI.
# sentence-transformers depends on torch — since we already installed it
# above, pip sees it's satisfied and won't re-download a CPU version.
#
# Why COPY requirements.txt before the rest of the code?
# Docker caches each layer. If your Python code changes but requirements.txt
# doesn't, Docker reuses the cached dependency layer and skips the slow
# pip install. This is called "layer caching" — it makes rebuilds fast.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# ---- Application Code ----
# Copied last so code changes don't invalidate the dependency layers.
RUN groupadd -r vortex && useradd -r -g vortex -m vortex

COPY worker/ worker/

RUN mkdir -p /home/vortex/.cache && chown -R vortex:vortex /home/vortex/.cache

USER vortex

# ---- Entry Point ----
# -m runs worker/main.py as a module. This is the Python convention for
# running packages (vs. "python worker/main.py" which can have import issues).
CMD ["python", "-m", "worker.main"]
