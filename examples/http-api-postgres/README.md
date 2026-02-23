# Full stack seeded example

Services:

- `frontend` (http-echo)
- `api` (http-echo)
- `postgres` seeded with 10 rows

## 1) Start stack

```bash
docker compose -f examples/http-api-postgres/docker-compose.yml up -d
```

## 2) Discover hostnames

```bash
dnsvard doctor
```

Use `workspace_host` from output.

## 3) Verify HTTP

```bash
curl "http://<workspace_host>"
curl "http://api.<workspace_host>"
```

## 4) Verify Postgres seed

```bash
psql -h postgres.<workspace_host> -U postgres -d app -c 'select count(*) from demo_values'
psql -h postgres.<workspace_host> -U postgres -d app -c 'select * from demo_values order by value'
```

Expected row count is `10`.

## 5) Cleanup

```bash
docker compose -f examples/http-api-postgres/docker-compose.yml down -v
```
