# School Academic System — API (Go · Echo · PostgreSQL)

JSON API for accounts, news/announcements and student enrollment.

## Run locally
```bash
cp .env.example .env        # edit secrets first (JWT_SECRET >= 32 chars)
go mod tidy                 # generates go.sum - commit it
docker compose up --build   # API + Postgres on http://localhost:8080
```

## Roles
| Role | Can do |
|---|---|
| `user` (public sign-up) | register, apply for enrollment, upload own documents, see own status |
| `news_admin` | CRUD news and announcements |
| `enrollment_admin` | manage academic years, view all applications and documents, update status |
| `super_admin` | everything above + create admin accounts, change roles, deactivate users |

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
