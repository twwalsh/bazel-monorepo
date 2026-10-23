#!/usr/bin/env bash
# One-time GCP setup for the Bazel remote cache and keyless CI auth.
#
#   PROJECT=my-project ./scripts/gcp-setup.sh
#
# Creates: a GCS bucket for the cache (with a 14-day lifecycle so it never grows unbounded), a service account
# that may read and write that bucket and nothing else, and a Workload Identity Federation pool + provider so
# GitHub Actions in twwalsh/bazel-monorepo can impersonate that account with an OIDC token. No key files.
# Prints the two values the workflow needs as repository secrets.
set -euo pipefail

PROJECT="${PROJECT:?set PROJECT=<gcp project id>}"
REPO="${REPO:-twwalsh/bazel-monorepo}"
REGION="${REGION:-us-east1}"
BUCKET="${BUCKET:-${PROJECT}-bazel-cache}"
SA_NAME="bazel-cache"
SA="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
POOL="github"
PROVIDER="github-oidc"

gcloud config set project "$PROJECT" >/dev/null
PROJECT_NUMBER="$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')"

echo "== APIs"
gcloud services enable storage.googleapis.com iamcredentials.googleapis.com sts.googleapis.com iam.googleapis.com

echo "== bucket gs://$BUCKET"
if ! gcloud storage buckets describe "gs://$BUCKET" >/dev/null 2>&1; then
  gcloud storage buckets create "gs://$BUCKET" --location="$REGION" --uniform-bucket-level-access --public-access-prevention
fi
cat > /tmp/bazel-cache-lifecycle.json <<'EOF'
{"rule":[{"action":{"type":"Delete"},"condition":{"age":14}}]}
EOF
gcloud storage buckets update "gs://$BUCKET" --lifecycle-file=/tmp/bazel-cache-lifecycle.json

echo "== service account $SA"
if ! gcloud iam service-accounts describe "$SA" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$SA_NAME" --display-name="Bazel remote cache (CI)"
fi
# objectUser = read + write objects in this one bucket. No project-level role.
gcloud storage buckets add-iam-policy-binding "gs://$BUCKET" --member="serviceAccount:$SA" --role=roles/storage.objectUser >/dev/null

echo "== workload identity federation"
if ! gcloud iam workload-identity-pools describe "$POOL" --location=global >/dev/null 2>&1; then
  gcloud iam workload-identity-pools create "$POOL" --location=global --display-name="GitHub Actions"
fi
if ! gcloud iam workload-identity-pools providers describe "$PROVIDER" --location=global --workload-identity-pool="$POOL" >/dev/null 2>&1; then
  gcloud iam workload-identity-pools providers create-oidc "$PROVIDER" \
    --location=global --workload-identity-pool="$POOL" \
    --issuer-uri="https://token.actions.githubusercontent.com" \
    --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
    --attribute-condition="assertion.repository == '${REPO}'"
fi
# Only workflows from this repository may impersonate the account.
gcloud iam service-accounts add-iam-policy-binding "$SA" \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL}/attribute.repository/${REPO}" >/dev/null

WIF_PROVIDER="projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL}/providers/${PROVIDER}"
echo
echo "Done. Set these as GitHub repository secrets on $REPO:"
echo "  GCP_WIF_PROVIDER = $WIF_PROVIDER"
echo "  GCP_CACHE_SA     = $SA"
echo
echo "And in .bazelrc, replace __BUCKET__ with: $BUCKET"
echo "Local use: gcloud auth application-default login, then: bazel build --config=gcs //..."
