#!/usr/bin/env bash
set -euo pipefail

DATABASE_URL="${DATABASE_URL:-postgres://foghorn:foghorn@localhost:5432/foghorn?sslmode=disable}"

echo "Applying Foghorn migrations to $DATABASE_URL..."

for f in $(ls migrations/*.sql | sort); do
    echo "-> Running $f..."
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f"
done

echo "All migrations applied successfully."
