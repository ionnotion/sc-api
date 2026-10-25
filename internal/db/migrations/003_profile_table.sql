CREATE TABLE IF NOT EXISTS academy_profile (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    about_us         TEXT NOT NULL,
    vision           TEXT NOT NULL,
    mission          TEXT NOT NULL,
    address          TEXT NOT NULL,
    phone            TEXT NOT NULL,
    email            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);