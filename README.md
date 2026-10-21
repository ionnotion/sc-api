# School Academic System — API (Go · Echo · PostgreSQL)

JSON API for accounts, news/announcements and student enrollment, plus a few simple server-rendered pages.
The React/Vite frontend (public site + admin) is a **separate repository**: `school-academic-web`.

## Run locally
```bash
cp .env.example .env        # edit secrets first (JWT_SECRET >= 32 chars)
go mod tidy                 # generates go.sum - commit it
docker compose up --build   # API + Postgres on http://localhost:8080
```
A super admin is created on first start from `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD`.
Migrations in `internal/db/migrations/` run automatically at startup.

### With the frontend
- **Development:** run `npm run dev` in the frontend repo; it proxies `/api` to `localhost:8080` (no CORS needed).
- **Everything in Docker:** put both repos side by side and run
  `docker compose -f docker-compose.yml -f docker-compose.web.yml up --build`
  → public site `http://localhost:3000/`, admin `http://localhost:3000/admin/`, API `http://localhost:3000/api/...`.
- The Go server's own pages on `:8080` (`/`, `/about`, `/contact`, `/news`) duplicate the React public app; delete them if you don't need them.

## Roles
| Role | Can do |
|---|---|
| `user` (public sign-up) | register, apply for enrollment, upload own documents, see own status |
| `news_admin` | CRUD news and announcements |
| `enrollment_admin` | manage academic years, view all applications and documents, update status |
| `super_admin` | everything above + create admin accounts, change roles, deactivate users |

Admin accounts cannot self-register (anyone could otherwise make themselves an admin): a super admin creates them in the admin app under **Users & admins → Register a new admin** (`POST /api/admin/users`).
Roles are re-read from the database on every request, so demotion/deactivation is immediate.

## API
| Method & path | Access |
|---|---|
| `GET /`, `/about`, `/contact`, `/news`, `/news/:slug` | public pages |
| `POST /api/auth/register`, `POST /api/auth/login` | public (rate-limited) |
| `GET /api/news?type=news\|announcement&page=&limit=`, `GET /api/news/:slug` | public (published only) |
| `GET /api/academic-years` | public |
| `GET /api/me` | logged in |
| `POST /api/enrollments`, `GET /api/enrollments`, `GET /api/enrollments/:id` | logged in (owner) |
| `POST /api/enrollments/:id/documents` (multipart `doc_type`, `file`) | owner, while `pending`/`needs_revision` |
| `GET /api/enrollments/:id/documents/:docId` | owner or enrollment admin |
| `GET/POST/PUT/DELETE /api/admin/news[/:id]` | news_admin, super_admin |
| `POST /api/admin/academic-years`, `PUT /api/admin/academic-years/:id` | enrollment_admin, super_admin |
| `GET /api/admin/enrollments?status=&academic_year_id=` | enrollment_admin, super_admin |
| `PATCH /api/admin/enrollments/:id/status` `{status, note}` | enrollment_admin, super_admin |
| `POST/GET /api/admin/users`, `GET/PATCH/DELETE /api/admin/users/:id` | super_admin (delete fails if the user owns posts/applications; deactivate instead) |
| `GET /api/admin/news/:id` | news_admin, super_admin |
| `PUT /api/enrollments/:id`, `DELETE /api/enrollments/:id/documents/:docId` | owner while `pending`/`needs_revision`; enrollment admins any time |
| `DELETE /api/admin/enrollments/:id` | enrollment_admin, super_admin (also removes uploaded files) |

Enrollment statuses: `pending → under_review → needs_revision | accepted | rejected`
(every change is logged in `enrollment_status_history`). Documents: PDF/JPG/PNG, size limit `MAX_UPLOAD_MB`, type checked by content.

## Quick walkthrough
```bash
B=http://localhost:8080/api
TOKEN=$(curl -s $B/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"admin@yourschool.sch.id","password":"change-me-now-12345"}' | jq -r .token)

curl -s $B/admin/academic-years -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"2026/2027","start_date":"2026-07-13","end_date":"2027-06-26","registration_open":true}'

curl -s $B/admin/news -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"type":"announcement","title":"Enrollment is open","excerpt":"Register now","body":"Details...","is_published":true}'
```

## Deploy (single VPS)
```bash
# on the server, with Docker installed and a domain pointing at it
cp .env.example .env && nano .env        # real secrets, CORS_ORIGINS, SITE_*
nano deploy/Caddyfile                    # your domain
docker compose -f docker-compose.yml -f docker-compose.web.yml -f docker-compose.prod.yml up -d --build
```
Needs Docker Compose >= 2.24 (`!reset` in the prod override). Caddy provides HTTPS automatically and forwards everything to the frontend's nginx, which serves both apps and proxies `/api` to the Go container.
Schedule `deploy/backup.sh` with cron and copy backups off the server.

## Notes
- Uploads live on the `uploads` Docker volume. If you move to a multi-server or PaaS setup, switch to S3-compatible object storage.
- Production: remove the published `8080` port of `api` (the prod override already does) so only Caddy is exposed.
- Server-rendered news bodies are plain text (blank line = new paragraph) and HTML-escaped; add a sanitized Markdown renderer if you need rich text.
- Not included yet (natural next steps): password reset by email, email notification on status change, audit UI, automated tests.
