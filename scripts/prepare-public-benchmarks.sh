#!/bin/sh
# Clone the exact public revisions used by the compatibility record.
set -eu
if [ "$#" -ne 1 ]; then
  echo "Usage: sh scripts/prepare-public-benchmarks.sh NEW_DIRECTORY" >&2
  exit 2
fi

benchmark_dir=$1
mkdir -- "$benchmark_dir"
benchmark_dir=$(CDPATH= cd -- "$benchmark_dir" && pwd)

git clone https://github.com/open-telemetry/opentelemetry-demo.git "$benchmark_dir/opentelemetry-demo"
git -C "$benchmark_dir/opentelemetry-demo" checkout --detach 147ddb4fefe4978083bbaeb4d1efb14946c465ab
git clone https://github.com/GoogleCloudPlatform/microservices-demo.git "$benchmark_dir/online-boutique"
git -C "$benchmark_dir/online-boutique" checkout --detach b9a978db9e01f4ad3dca9494a22cb9edc17548fe

printf 'Prepared pinned public benchmarks at %s\n' "$benchmark_dir"
