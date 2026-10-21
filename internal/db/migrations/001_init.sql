CREATE TYPE user_role AS ENUM ('user', 'news_admin', 'enrollment_admin', 'super_admin');
CREATE TYPE post_type AS ENUM ('news', 'announcement');
CREATE TYPE enrollment_status AS ENUM ('pending', 'under_review', 'needs_revision', 'accepted', 'rejected');

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name     TEXT NOT NULL,
    email         TEXT NOT NULL UNIQUE,
    phone         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          user_role NOT NULL DEFAULT 'user',
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE posts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type         post_type NOT NULL,
    title        TEXT NOT NULL,
    slug         TEXT NOT NULL UNIQUE,
    excerpt      TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL,
    is_published BOOLEAN NOT NULL DEFAULT FALSE,
    published_at TIMESTAMPTZ,
    author_id    UUID NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX posts_published_idx ON posts (is_published, published_at DESC);

CREATE TABLE academic_years (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT NOT NULL UNIQUE,          -- e.g. 2026/2027
    start_date        DATE NOT NULL,
    end_date          DATE NOT NULL CHECK (end_date > start_date),
    registration_open BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE enrollments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    academic_year_id UUID NOT NULL REFERENCES academic_years(id),
    applicant_id     UUID NOT NULL REFERENCES users(id),
    student_name     TEXT NOT NULL,
    birth_date       DATE NOT NULL,
    gender           TEXT NOT NULL CHECK (gender IN ('male', 'female')),
    previous_school  TEXT NOT NULL DEFAULT '',
    target_level     TEXT NOT NULL,
    parent_name      TEXT NOT NULL,
    parent_phone     TEXT NOT NULL,
    address          TEXT NOT NULL,
    status           enrollment_status NOT NULL DEFAULT 'pending',
    admin_note       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX enrollments_applicant_idx ON enrollments (applicant_id);
CREATE INDEX enrollments_year_status_idx ON enrollments (academic_year_id, status);

CREATE TABLE enrollment_documents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enrollment_id UUID NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
    doc_type      TEXT NOT NULL,
    original_name TEXT NOT NULL,
    stored_name   TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    size_bytes    BIGINT NOT NULL,
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX enrollment_documents_enrollment_idx ON enrollment_documents (enrollment_id);

CREATE TABLE enrollment_status_history (
    id            BIGSERIAL PRIMARY KEY,
    enrollment_id UUID NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
    from_status   enrollment_status NOT NULL,
    to_status     enrollment_status NOT NULL,
    note          TEXT NOT NULL DEFAULT '',
    changed_by    UUID NOT NULL REFERENCES users(id),
    changed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
