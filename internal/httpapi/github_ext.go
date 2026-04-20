package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

const maxBatchObjectRead = 100

type batchObjectReadRequest struct {
	Objects []batchObjectRef `json:"objects"`
}

type batchObjectRef struct {
	Type   string `json:"type"`
	Number int    `json:"number"`
}

type batchObjectReadResponse struct {
	Results []batchObjectReadResult `json:"results"`
}

type batchObjectReadResult struct {
	Type   string          `json:"type"`
	Number int             `json:"number"`
	Found  bool            `json:"found"`
	Object json.RawMessage `json:"object,omitempty"`
}

func (s *Server) handleBatchReadObjects(c echo.Context) error {
	repoID, err := findRepositoryID(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	var input batchObjectReadRequest
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid request body"})
	}

	if len(input.Objects) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Objects are required"})
	}
	if len(input.Objects) > maxBatchObjectRead {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Too many objects"})
	}

	issueNumbers := make([]int, 0, len(input.Objects))
	issueSeen := map[int]struct{}{}
	pullNumbers := make([]int, 0, len(input.Objects))
	pullSeen := map[int]struct{}{}
	for _, object := range input.Objects {
		if object.Number <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid object number"})
		}
		switch object.Type {
		case "issue":
			if _, ok := issueSeen[object.Number]; !ok {
				issueSeen[object.Number] = struct{}{}
				issueNumbers = append(issueNumbers, object.Number)
			}
		case "pull_request":
			if _, ok := pullSeen[object.Number]; !ok {
				pullSeen[object.Number] = struct{}{}
				pullNumbers = append(pullNumbers, object.Number)
			}
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{"message": "Unsupported object type"})
		}
	}

	issuesByNumber, err := s.loadIssuesByNumber(c.Request().Context(), repoID, issueNumbers)
	if err != nil {
		return err
	}
	pullsByNumber, err := s.loadPullRequestsByNumber(c.Request().Context(), repoID, pullNumbers)
	if err != nil {
		return err
	}

	results := make([]batchObjectReadResult, 0, len(input.Objects))
	for _, object := range input.Objects {
		result := batchObjectReadResult{
			Type:   object.Type,
			Number: object.Number,
		}
		switch object.Type {
		case "issue":
			raw, ok := issuesByNumber[object.Number]
			if ok {
				result.Found = true
				result.Object = raw
			}
		case "pull_request":
			raw, ok := pullsByNumber[object.Number]
			if ok {
				result.Found = true
				result.Object = raw
			}
		}
		results = append(results, result)
	}

	return c.JSON(http.StatusOK, batchObjectReadResponse{Results: results})
}

func (s *Server) loadIssuesByNumber(ctx context.Context, repositoryID uint, numbers []int) (map[int]json.RawMessage, error) {
	if len(numbers) == 0 {
		return map[int]json.RawMessage{}, nil
	}
	var issues []struct {
		Number  int    `gorm:"column:number"`
		RawJSON []byte `gorm:"column:raw_json"`
	}
	if err := s.db.WithContext(ctx).
		Model(&database.Issue{}).
		Select("number", "raw_json").
		Where("repository_id = ? AND number IN ?", repositoryID, numbers).
		Find(&issues).Error; err != nil {
		return nil, err
	}
	out := make(map[int]json.RawMessage, len(issues))
	for _, issue := range issues {
		out[issue.Number] = json.RawMessage(issue.RawJSON)
	}
	return out, nil
}

func (s *Server) loadPullRequestsByNumber(ctx context.Context, repositoryID uint, numbers []int) (map[int]json.RawMessage, error) {
	if len(numbers) == 0 {
		return map[int]json.RawMessage{}, nil
	}
	var pulls []struct {
		Number  int    `gorm:"column:number"`
		RawJSON []byte `gorm:"column:raw_json"`
	}
	if err := s.db.WithContext(ctx).
		Model(&database.PullRequest{}).
		Select("number", "raw_json").
		Where("repository_id = ? AND number IN ?", repositoryID, numbers).
		Find(&pulls).Error; err != nil {
		return nil, err
	}
	out := make(map[int]json.RawMessage, len(pulls))
	for _, pull := range pulls {
		out[pull.Number] = json.RawMessage(pull.RawJSON)
	}
	return out, nil
}
