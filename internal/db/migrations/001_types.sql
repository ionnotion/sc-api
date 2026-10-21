CREATE TYPE user_role AS ENUM ('user', 'news_admin', 'enrollment_admin', 'super_admin');
CREATE TYPE post_type AS ENUM ('news', 'announcement');
CREATE TYPE enrollment_status AS ENUM ('pending', 'under_review', 'needs_revision', 'accepted', 'rejected');