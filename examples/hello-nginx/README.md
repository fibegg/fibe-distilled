# hello-nginx

A smallest routed Playground for checking a Marquee, Traefik, and service URL generation.

Create a Playspec from this file, then create a Playground with a `marquee_id`:

```sh
curl -sS -X POST "$FIBE_DOMAIN/api/playspecs" \
  -H "Authorization: Bearer $FIBE_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"playspec\":{\"name\":\"hello-nginx\",\"base_compose_yaml\":$(jq -Rs . < docker-compose.yml)}}"
```
