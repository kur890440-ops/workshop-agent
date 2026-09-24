package background

import "database/sql"

type Repository struct{ DB *sql.DB }

const jobColumns = `id,workshop_id,created_by_user_id,job_type,status,schedule_type,local_time,timezone,parameters_json,notification_enabled,next_run_at,last_run_at,last_success_at,last_result,created_at,updated_at`

func scanJob(row interface{ Scan(...any) error }) (j Job, e error) {
	e = row.Scan(&j.ID, &j.WorkshopID, &j.CreatedBy, &j.Type, &j.Status, &j.Schedule, &j.LocalTime, &j.Timezone, &j.Parameters, &j.NotificationEnabled, &j.NextRun, &j.LastRun, &j.LastSuccess, &j.LastResult, &j.CreatedAt, &j.UpdatedAt)
	return
}
func (r *Repository) Get(workshop, id int64) (Job, error) {
	return scanJob(r.DB.QueryRow(`SELECT `+jobColumns+` FROM background_jobs WHERE id=? AND workshop_id=?`, id, workshop))
}
func (r *Repository) List(workshop int64) ([]Job, error) {
	rows, e := r.DB.Query(`SELECT `+jobColumns+` FROM background_jobs WHERE workshop_id=? ORDER BY id`, workshop)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
