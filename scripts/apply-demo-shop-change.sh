#!/bin/sh
# Apply the canonical checkout request-contract change to a generated demo.
set -eu
if [ "$#" -ne 1 ]; then
  echo "Usage: sh scripts/apply-demo-shop-change.sh DEMO_DIRECTORY" >&2
  exit 2
fi
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
checkout=$1/repositories/checkout
test -d "$checkout/.git"
cp "$root/examples/demo-shop/scenarios/checkout-contract-v2/CheckoutRequest.java" \
  "$checkout/src/main/java/example/CheckoutRequest.java"
cp "$root/examples/demo-shop/scenarios/checkout-contract-v2/openapi.yaml" \
  "$checkout/openapi.yaml"
git -C "$checkout" add src/main/java/example/CheckoutRequest.java openapi.yaml
git -C "$checkout" -c user.name='Demo Developer' \
  -c user.email='developer@example.test' -c commit.gpgsign=false commit -q \
  -m 'Evolve checkout request contract'
printf 'Applied checkout contract v2 in %s\n' "$checkout"
