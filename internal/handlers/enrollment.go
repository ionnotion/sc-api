package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"schoolapp/internal/authmw"
)

var (
	enrollmentStatuses = map[string]bool{"pending": true, "under_review": true, "needs_revision": true, "accepted": true, "rejected": true}
	docTypes           = map[string]bool{"birth_certificate": true, "family_card": true, "photo": true, "report_card": true, "other": true}
	allowedMIME        = map[string]string{"application/pdf": ".pdf", "image/jpeg": ".jpg", "image/png": ".png"}
)

const maxDocsPerEnrollment = 10

func isEnrollmentAdmin(role string) bool {
	return role == authmw.RoleEnrollmentAdmin || role == authmw.RoleSuperAdmin
}

func canAccess(c echo.Context, owner uuid.UUID) bool {
	return owner == authmw.UserID(c) || isEnrollmentAdmin(authmw.Role(c))
}

// ---------------------------------------------------------------- academic years

type AcademicYear struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	StartDate        string    `json:"start_date"`
	EndDate          string    `json:"end_date"`
	RegistrationOpen bool      `json:"registration_open"`
}

const yearCols = `id, name, start_date::text, end_date::text, registration_open`

func scanYear(s scanner, y *AcademicYear) error {
	return s.Scan(&y.ID, &y.Name, &y.StartDate, &y.EndDate, &y.RegistrationOpen)
}

type yearReq struct {
	Name             string `json:"name"`
	StartDate        string `json:"start_date"`
	EndDate          string `json:"end_date"`
	RegistrationOpen bool   `json:"registration_open"`
}

func (r *yearReq) validate() error {
	r.Name = strings.TrimSpace(r.Name)
	s, err1 := time.Parse("2006-01-02", r.StartDate)
	e, err2 := time.Parse("2006-01-02", r.EndDate)
	if r.Name == "" || err1 != nil || err2 != nil {
		return invalid("name, start_date and end_date (YYYY-MM-DD) are required")
	}
	if !e.After(s) {
		return invalid("end_date must be after start_date")
	}
	return nil
}

// GET /api/academic-years  (public)
func (h *Handler) ListAcademicYears(c echo.Context) error {
	rows, err := h.DB.Query(c.Request().Context(), `SELECT `+yearCols+` FROM academic_years ORDER BY start_date DESC`)
	if err != nil {
		return internalErr(err)
	}
	defer rows.Close()
	out := []AcademicYear{}
	for rows.Next() {
		var y AcademicYear
		if err := scanYear(rows, &y); err != nil {
			return internalErr(err)
		}
		out = append(out, y)
	}
	return c.JSON(http.StatusOK, out)
}

// POST /api/admin/academic-years
func (h *Handler) CreateAcademicYear(c echo.Context) error {
	var r yearReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	var y AcademicYear
	err := scanYear(h.DB.QueryRow(c.Request().Context(),
		`INSERT INTO academic_years (name, start_date, end_date, registration_open)
		 VALUES ($1, $2::date, $3::date, $4) RETURNING `+yearCols,
		r.Name, r.StartDate, r.EndDate, r.RegistrationOpen), &y)
	if isUniqueViolation(err) {
		return echo.NewHTTPError(http.StatusConflict, "academic year already exists")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusCreated, y)
}

// PUT /api/admin/academic-years/:id  (also used to open/close registration)
func (h *Handler) UpdateAcademicYear(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	var r yearReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	var y AcademicYear
	err = scanYear(h.DB.QueryRow(c.Request().Context(),
		`UPDATE academic_years SET name=$2, start_date=$3::date, end_date=$4::date, registration_open=$5
		 WHERE id=$1 RETURNING `+yearCols,
		id, r.Name, r.StartDate, r.EndDate, r.RegistrationOpen), &y)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("academic year")
	}
	if isUniqueViolation(err) {
		return echo.NewHTTPError(http.StatusConflict, "academic year already exists")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, y)
}

// ---------------------------------------------------------------- enrollments

type Document struct {
	ID           uuid.UUID `json:"id"`
	DocType      string    `json:"doc_type"`
	OriginalName string    `json:"original_name"`
	ContentType  string    `json:"content_type"`
	SizeBytes    int64     `json:"size_bytes"`
	UploadedAt   time.Time `json:"uploaded_at"`
}

type Enrollment struct {
	ID             uuid.UUID  `json:"id"`
	AcademicYearID uuid.UUID  `json:"academic_year_id"`
	AcademicYear   string     `json:"academic_year"`
	ApplicantID    uuid.UUID  `json:"applicant_id"`
	ApplicantName  string     `json:"applicant_name"`
	StudentName    string     `json:"student_name"`
	BirthDate      string     `json:"birth_date"`
	Gender         string     `json:"gender"`
	PreviousSchool string     `json:"previous_school"`
	TargetLevel    string     `json:"target_level"`
	ParentName     string     `json:"parent_name"`
	ParentPhone    string     `json:"parent_phone"`
	Address        string     `json:"address"`
	Status         string     `json:"status"`
	AdminNote      string     `json:"admin_note"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Documents      []Document `json:"documents,omitempty"`
}

const enrollmentSelect = `SELECT e.id, e.academic_year_id, ay.name as academic_year, e.applicant_id, CONCAT(u.first_name, '', u. last_name) as applicant_name,
	e.student_name, e.birth_date::text, e.gender, e.previous_school, e.target_level,
	e.parent_name, e.parent_phone, e.address, e.status::text, e.admin_note, e.created_at, e.updated_at
	FROM enrollments e
	JOIN academic_years ay ON ay.id = e.academic_year_id
	JOIN users u ON u.id = e.applicant_id`

func scanEnrollment(s scanner, e *Enrollment) error {
	return s.Scan(&e.ID, &e.AcademicYearID, &e.AcademicYear, &e.ApplicantID, &e.ApplicantName,
		&e.StudentName, &e.BirthDate, &e.Gender, &e.PreviousSchool, &e.TargetLevel,
		&e.ParentName, &e.ParentPhone, &e.Address, &e.Status, &e.AdminNote, &e.CreatedAt, &e.UpdatedAt)
}

func (h *Handler) listEnrollments(ctx context.Context, where []string, args []any, limit, offset int) ([]Enrollment, error) {
	args = append(args, limit, offset)
	q := enrollmentSelect + " WHERE " + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := h.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Enrollment{}
	for rows.Next() {
		var e Enrollment
		if err := scanEnrollment(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (h *Handler) getEnrollment(ctx context.Context, id uuid.UUID) (Enrollment, error) {
	var e Enrollment
	if err := scanEnrollment(h.DB.QueryRow(ctx, enrollmentSelect+` WHERE e.id=$1`, id), &e); err != nil {
		return e, err
	}
	rows, err := h.DB.Query(ctx,
		`SELECT id, doc_type, original_name, content_type, size_bytes, uploaded_at
		 FROM enrollment_documents WHERE enrollment_id=$1 ORDER BY uploaded_at`, id)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.DocType, &d.OriginalName, &d.ContentType, &d.SizeBytes, &d.UploadedAt); err != nil {
			return e, err
		}
		e.Documents = append(e.Documents, d)
	}
	return e, rows.Err()
}

type enrollmentReq struct {
	AcademicYearID string `json:"academic_year_id"`
	StudentName    string `json:"student_name"`
	BirthDate      string `json:"birth_date"`
	Gender         string `json:"gender"`
	PreviousSchool string `json:"previous_school"`
	TargetLevel    string `json:"target_level"`
	ParentName     string `json:"parent_name"`
	ParentPhone    string `json:"parent_phone"`
	Address        string `json:"address"`
}

func (r *enrollmentReq) validateFields() error {
	for _, f := range []*string{&r.StudentName, &r.PreviousSchool, &r.TargetLevel, &r.ParentName, &r.ParentPhone, &r.Address} {
		*f = strings.TrimSpace(*f)
	}
	if _, err := time.Parse("2006-01-02", r.BirthDate); err != nil {
		return invalid("birth_date must be YYYY-MM-DD")
	}
	if r.Gender != "male" && r.Gender != "female" {
		return invalid("gender must be male or female")
	}
	if r.StudentName == "" || r.TargetLevel == "" || r.ParentName == "" || r.ParentPhone == "" || r.Address == "" {
		return invalid("student_name, target_level, parent_name, parent_phone and address are required")
	}
	return nil
}

// POST /api/enrollments  (any logged-in user; only while the year's registration is open)
func (h *Handler) CreateEnrollment(c echo.Context) error {
	var r enrollmentReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validateFields(); err != nil {
		return err
	}
	yearID, err := uuid.Parse(r.AcademicYearID)
	if err != nil {
		return invalid("academic_year_id is invalid")
	}

	ctx := c.Request().Context()
	var open bool
	err = h.DB.QueryRow(ctx, `SELECT registration_open FROM academic_years WHERE id=$1`, yearID).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid("academic year not found")
	}
	if err != nil {
		return internalErr(err)
	}
	if !open {
		return echo.NewHTTPError(http.StatusConflict, "registration for this academic year is closed")
	}

	var id uuid.UUID
	err = h.DB.QueryRow(ctx,
		`INSERT INTO enrollments (academic_year_id, applicant_id, student_name, birth_date, gender,
		   previous_school, target_level, parent_name, parent_phone, address)
		 VALUES ($1,$2,$3,$4::date,$5,$6,$7,$8,$9,$10) RETURNING id`,
		yearID, authmw.UserID(c), r.StudentName, r.BirthDate, r.Gender,
		r.PreviousSchool, r.TargetLevel, r.ParentName, r.ParentPhone, r.Address).Scan(&id)
	if err != nil {
		return internalErr(err)
	}

	e, err := h.getEnrollment(ctx, id)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusCreated, e)
}

// GET /api/enrollments  (the caller's own applications)
func (h *Handler) MyEnrollments(c echo.Context) error {
	limit, offset := paging(c)
	list, err := h.listEnrollments(c.Request().Context(), []string{"e.applicant_id = $1"}, []any{authmw.UserID(c)}, limit, offset)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, list)
}

// GET /api/enrollments/:id  (owner or enrollment admin)
func (h *Handler) GetEnrollment(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	e, err := h.getEnrollment(c.Request().Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canAccess(c, e.ApplicantID)) {
		return notFound("enrollment")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, e)
}

// GET /api/admin/enrollments?status=&academic_year_id=&page=&limit=
func (h *Handler) ListEnrollmentsAdmin(c echo.Context) error {
	limit, offset := paging(c)
	where := []string{"TRUE"}
	args := []any{}
	if s := c.QueryParam("status"); s != "" {
		if !enrollmentStatuses[s] {
			return invalid("unknown status")
		}
		args = append(args, s)
		where = append(where, fmt.Sprintf("e.status = $%d::enrollment_status", len(args)))
	}
	if y := c.QueryParam("academic_year_id"); y != "" {
		yid, err := uuid.Parse(y)
		if err != nil {
			return invalid("academic_year_id is invalid")
		}
		args = append(args, yid)
		where = append(where, fmt.Sprintf("e.academic_year_id = $%d", len(args)))
	}
	list, err := h.listEnrollments(c.Request().Context(), where, args, limit, offset)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, list)
}

// PATCH /api/admin/enrollments/:id/status  {status, note}
func (h *Handler) UpdateEnrollmentStatus(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	var r struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	r.Note = strings.TrimSpace(r.Note)
	if !enrollmentStatuses[r.Status] {
		return invalid("status must be one of: pending, under_review, needs_revision, accepted, rejected")
	}
	if (r.Status == "needs_revision" || r.Status == "rejected") && r.Note == "" {
		return invalid("note is required when requesting revision or rejecting")
	}

	ctx := c.Request().Context()
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		return internalErr(err)
	}
	defer tx.Rollback(ctx)

	var from string
	err = tx.QueryRow(ctx, `SELECT status::text FROM enrollments WHERE id=$1 FOR UPDATE`, id).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("enrollment")
	}
	if err != nil {
		return internalErr(err)
	}
	if _, err = tx.Exec(ctx,
		`UPDATE enrollments SET status=$2::enrollment_status, admin_note=$3, updated_at=now() WHERE id=$1`,
		id, r.Status, r.Note); err != nil {
		return internalErr(err)
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO enrollment_status_history (enrollment_id, from_status, to_status, note, changed_by)
		 VALUES ($1, $2::enrollment_status, $3::enrollment_status, $4, $5)`,
		id, from, r.Status, r.Note, authmw.UserID(c)); err != nil {
		return internalErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return internalErr(err)
	}
	e, err := h.getEnrollment(ctx, id)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, e)
}

// ---------------------------------------------------------------- documents

// POST /api/enrollments/:id/documents  (multipart: doc_type, file) - owner only,
// while status is pending or needs_revision.
func (h *Handler) UploadDocument(c echo.Context) error {
	ctx := c.Request().Context()
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	var owner uuid.UUID
	var status string
	err = h.DB.QueryRow(ctx, `SELECT applicant_id, status::text FROM enrollments WHERE id=$1`, id).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != authmw.UserID(c)) {
		return notFound("enrollment")
	}
	if err != nil {
		return internalErr(err)
	}
	if status != "pending" && status != "needs_revision" {
		return echo.NewHTTPError(http.StatusConflict, "documents can no longer be changed for this application")
	}
	docType := c.FormValue("doc_type")
	if !docTypes[docType] {
		return invalid("doc_type must be one of: birth_certificate, family_card, photo, report_card, other")
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "file is required")
	}
	maxBytes := h.Cfg.MaxUploadMB << 20
	if fh.Size > maxBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("file exceeds %d MB", h.Cfg.MaxUploadMB))
	}
	var count int
	if err := h.DB.QueryRow(ctx, `SELECT count(*) FROM enrollment_documents WHERE enrollment_id=$1`, id).Scan(&count); err != nil {
		return internalErr(err)
	}
	if count >= maxDocsPerEnrollment {
		return echo.NewHTTPError(http.StatusConflict, "document limit reached")
	}

	src, err := fh.Open()
	if err != nil {
		return internalErr(err)
	}
	defer src.Close()

	// Trust the file's content, not its name or the client-supplied type.
	head := make([]byte, 512)
	n, _ := io.ReadFull(src, head)
	mime := http.DetectContentType(head[:n])
	ext, ok := allowedMIME[mime]
	if !ok {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "only PDF, JPG and PNG files are allowed")
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return internalErr(err)
	}

	stored := uuid.NewString() + ext
	path := filepath.Join(h.Cfg.UploadDir, stored)
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return internalErr(err)
	}
	size, err := io.Copy(dst, io.LimitReader(src, maxBytes))
	dst.Close()
	if err != nil {
		os.Remove(path)
		return internalErr(err)
	}

	name := filepath.Base(fh.Filename)
	if len(name) > 150 {
		name = name[len(name)-150:]
	}
	var d Document
	err = h.DB.QueryRow(ctx,
		`INSERT INTO enrollment_documents (enrollment_id, doc_type, original_name, stored_name, content_type, size_bytes)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, doc_type, original_name, content_type, size_bytes, uploaded_at`,
		id, docType, name, stored, mime, size).Scan(&d.ID, &d.DocType, &d.OriginalName, &d.ContentType, &d.SizeBytes, &d.UploadedAt)
	if err != nil {
		os.Remove(path)
		return internalErr(err)
	}
	return c.JSON(http.StatusCreated, d)
}

// GET /api/enrollments/:id/documents/:docId  (owner or enrollment admin)
func (h *Handler) DownloadDocument(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	docID, err := idParam(c, "docId")
	if err != nil {
		return err
	}
	var owner uuid.UUID
	var stored, original string
	err = h.DB.QueryRow(c.Request().Context(),
		`SELECT e.applicant_id, d.stored_name, d.original_name
		 FROM enrollment_documents d JOIN enrollments e ON e.id = d.enrollment_id
		 WHERE d.id=$1 AND d.enrollment_id=$2`, docID, id).Scan(&owner, &stored, &original)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canAccess(c, owner)) {
		return notFound("document")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.Attachment(filepath.Join(h.Cfg.UploadDir, filepath.Base(stored)), original)
}

func editable(status string) bool { return status == "pending" || status == "needs_revision" }

// PUT /api/enrollments/:id  (owner while pending/needs_revision; enrollment admins any time)
func (h *Handler) UpdateEnrollment(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	var r enrollmentReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validateFields(); err != nil {
		return err
	}
	ctx := c.Request().Context()
	var owner uuid.UUID
	var status string
	err = h.DB.QueryRow(ctx, `SELECT applicant_id, status::text FROM enrollments WHERE id=$1`, id).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canAccess(c, owner)) {
		return notFound("enrollment")
	}
	if err != nil {
		return internalErr(err)
	}
	if !isEnrollmentAdmin(authmw.Role(c)) && !editable(status) {
		return echo.NewHTTPError(http.StatusConflict, "this application can no longer be edited")
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE enrollments SET student_name=$2, birth_date=$3::date, gender=$4, previous_school=$5,
		   target_level=$6, parent_name=$7, parent_phone=$8, address=$9, updated_at=now() WHERE id=$1`,
		id, r.StudentName, r.BirthDate, r.Gender, r.PreviousSchool, r.TargetLevel, r.ParentName, r.ParentPhone, r.Address); err != nil {
		return internalErr(err)
	}
	e, err := h.getEnrollment(ctx, id)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, e)
}

// DELETE /api/enrollments/:id/documents/:docId  (owner while editable; enrollment admins any time)
func (h *Handler) DeleteDocument(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	docID, err := idParam(c, "docId")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	var owner uuid.UUID
	var status, stored string
	err = h.DB.QueryRow(ctx,
		`SELECT e.applicant_id, e.status::text, d.stored_name
		 FROM enrollment_documents d JOIN enrollments e ON e.id = d.enrollment_id
		 WHERE d.id=$1 AND d.enrollment_id=$2`, docID, id).Scan(&owner, &status, &stored)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canAccess(c, owner)) {
		return notFound("document")
	}
	if err != nil {
		return internalErr(err)
	}
	if !isEnrollmentAdmin(authmw.Role(c)) && !editable(status) {
		return echo.NewHTTPError(http.StatusConflict, "documents can no longer be changed for this application")
	}
	if _, err := h.DB.Exec(ctx, `DELETE FROM enrollment_documents WHERE id=$1`, docID); err != nil {
		return internalErr(err)
	}
	os.Remove(filepath.Join(h.Cfg.UploadDir, filepath.Base(stored)))
	return c.NoContent(http.StatusNoContent)
}

// DELETE /api/admin/enrollments/:id  (enrollment_admin, super_admin) - removes documents too
func (h *Handler) DeleteEnrollment(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	rows, err := h.DB.Query(ctx, `SELECT stored_name FROM enrollment_documents WHERE enrollment_id=$1`, id)
	if err != nil {
		return internalErr(err)
	}
	var files []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			rows.Close()
			return internalErr(err)
		}
		files = append(files, f)
	}
	rows.Close()
	tag, err := h.DB.Exec(ctx, `DELETE FROM enrollments WHERE id=$1`, id)
	if err != nil {
		return internalErr(err)
	}
	if tag.RowsAffected() == 0 {
		return notFound("enrollment")
	}
	for _, f := range files {
		os.Remove(filepath.Join(h.Cfg.UploadDir, filepath.Base(f)))
	}
	return c.NoContent(http.StatusNoContent)
}
