package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"taskboard/internal/domain"
)

var validTemplateKinds = map[string]bool{"bug": true, "feature": true, "review": true}

func validateTaskTemplateInput(kind string, input map[string]string, required []string) error {
	if !validTemplateKinds[kind] {
		return errors.New("invalid task template kind")
	}
	for _, field := range required {
		if strings.TrimSpace(input[field]) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	return nil
}

func (s *Store) TaskTemplates(c context.Context, boardID string) ([]domain.TaskTemplate, error) {
	rows, err := s.DB.Query(c, `SELECT id,board_id,name,kind,description,required_fields,default_priority,default_label_ids,version,enabled,created_at,updated_at FROM task_templates WHERE board_id=$1 ORDER BY name`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.TaskTemplate])
}

func (s *Store) TaskTemplate(c context.Context, boardID, id string) (domain.TaskTemplate, error) {
	var template domain.TaskTemplate
	err := s.DB.QueryRow(c, `SELECT id,board_id,name,kind,description,required_fields,default_priority,default_label_ids,version,enabled,created_at,updated_at FROM task_templates WHERE board_id=$1 AND id=$2`, boardID, id).Scan(
		&template.ID, &template.BoardID, &template.Name, &template.Kind, &template.Description, &template.RequiredFields, &template.DefaultPriority, &template.DefaultLabelIDs, &template.Version, &template.Enabled, &template.CreatedAt, &template.UpdatedAt,
	)
	return template, err
}

func (s *Store) SaveTaskTemplate(c context.Context, template domain.TaskTemplate) (domain.TaskTemplate, error) {
	if !validTemplateKinds[template.Kind] {
		return domain.TaskTemplate{}, errors.New("invalid task template kind")
	}
	if strings.TrimSpace(template.Name) == "" {
		return domain.TaskTemplate{}, errors.New("task template name is required")
	}
	if !validPriority(template.DefaultPriority) {
		return domain.TaskTemplate{}, errors.New("invalid priority")
	}
	if template.RequiredFields == nil {
		template.RequiredFields = []string{}
	}
	if template.DefaultLabelIDs == nil {
		template.DefaultLabelIDs = []string{}
	}
	if err := s.validateTemplateLabels(c, template.BoardID, template.DefaultLabelIDs); err != nil {
		return domain.TaskTemplate{}, err
	}
	var result domain.TaskTemplate
	err := s.DB.QueryRow(c, `INSERT INTO task_templates(board_id,name,kind,description,required_fields,default_priority,default_label_ids) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,board_id,name,kind,description,required_fields,default_priority,default_label_ids,version,enabled,created_at,updated_at`, template.BoardID, strings.TrimSpace(template.Name), template.Kind, template.Description, template.RequiredFields, template.DefaultPriority, template.DefaultLabelIDs).Scan(
		&result.ID, &result.BoardID, &result.Name, &result.Kind, &result.Description, &result.RequiredFields, &result.DefaultPriority, &result.DefaultLabelIDs, &result.Version, &result.Enabled, &result.CreatedAt, &result.UpdatedAt,
	)
	return result, err
}

func (s *Store) UpdateTaskTemplate(c context.Context, template domain.TaskTemplate) error {
	if !validTemplateKinds[template.Kind] || strings.TrimSpace(template.Name) == "" || !validPriority(template.DefaultPriority) {
		return errors.New("invalid task template")
	}
	if err := s.validateTemplateLabels(c, template.BoardID, template.DefaultLabelIDs); err != nil {
		return err
	}
	_, err := s.DB.Exec(c, `UPDATE task_templates SET name=$3,kind=$4,description=$5,required_fields=$6,default_priority=$7,default_label_ids=$8,version=version+1,updated_at=now() WHERE board_id=$1 AND id=$2`, template.BoardID, template.ID, strings.TrimSpace(template.Name), template.Kind, template.Description, template.RequiredFields, template.DefaultPriority, template.DefaultLabelIDs)
	return err
}

func (s *Store) validateTemplateLabels(c context.Context, boardID string, labelIDs []string) error {
	for _, labelID := range labelIDs {
		var exists bool
		if err := s.DB.QueryRow(c, "SELECT EXISTS(SELECT 1 FROM labels WHERE id=$1 AND board_id=$2)", labelID, boardID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("template label does not belong to board")
		}
	}
	return nil
}

func (s *Store) SetTaskTemplateEnabled(c context.Context, boardID, id string, enabled bool) error {
	_, err := s.DB.Exec(c, `UPDATE task_templates SET enabled=$3,updated_at=now() WHERE board_id=$1 AND id=$2`, boardID, id, enabled)
	return err
}

func templateSnapshot(template domain.TaskTemplate) []byte {
	snapshot, _ := json.Marshal(template)
	return snapshot
}
