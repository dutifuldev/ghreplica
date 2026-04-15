package searchindex

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidMentionRequest = errors.New("invalid mention search request")

type Service struct {
	db *gorm.DB
}

type scoredDocument struct {
	database.SearchDocument
	Score float64 `gorm:"column:score"`
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func IsInvalidRequest(err error) bool {
	return errors.Is(err, ErrInvalidMentionRequest)
}

func (s *Service) RebuildRepository(ctx context.Context, owner, repo string) error {
	var repository database.Repository
	if err := s.db.WithContext(ctx).Where("full_name = ?", strings.TrimSpace(owner)+"/"+strings.TrimSpace(repo)).First(&repository).Error; err != nil {
		return err
	}
	return s.RebuildRepositoryByID(ctx, repository.ID)
}

func (s *Service) RebuildRepositoryByID(ctx context.Context, repositoryID uint) error {
	var issues []database.Issue
	if err := s.db.WithContext(ctx).
		Preload("Author").
		Where("repository_id = ? AND is_pull_request = ?", repositoryID, false).
		Find(&issues).Error; err != nil {
		return err
	}

	var pulls []database.PullRequest
	if err := s.db.WithContext(ctx).
		Preload("Issue").
		Preload("Issue.Author").
		Where("repository_id = ?", repositoryID).
		Find(&pulls).Error; err != nil {
		return err
	}

	var issueComments []database.IssueComment
	if err := s.db.WithContext(ctx).
		Preload("Author").
		Preload("Issue").
		Where("repository_id = ?", repositoryID).
		Find(&issueComments).Error; err != nil {
		return err
	}

	var reviews []database.PullRequestReview
	if err := s.db.WithContext(ctx).
		Preload("Author").
		Preload("PullRequest").
		Preload("PullRequest.Issue").
		Where("repository_id = ?", repositoryID).
		Find(&reviews).Error; err != nil {
		return err
	}

	var reviewComments []database.PullRequestReviewComment
	if err := s.db.WithContext(ctx).
		Preload("Author").
		Preload("PullRequest").
		Preload("PullRequest.Issue").
		Where("repository_id = ?", repositoryID).
		Find(&reviewComments).Error; err != nil {
		return err
	}

	docs := make([]database.SearchDocument, 0, len(issues)+len(pulls)+len(issueComments)+len(reviews)+len(reviewComments))
	for _, issue := range issues {
		if doc, ok := buildIssueDocument(issue); ok {
			docs = append(docs, doc)
		}
	}
	for _, pull := range pulls {
		if doc, ok := buildPullRequestDocument(pull); ok {
			docs = append(docs, doc)
		}
	}
	for _, comment := range issueComments {
		if doc, ok := buildIssueCommentDocument(comment); ok {
			docs = append(docs, doc)
		}
	}
	for _, review := range reviews {
		if doc, ok := buildPullRequestReviewDocument(review); ok {
			docs = append(docs, doc)
		}
	}
	for _, comment := range reviewComments {
		if doc, ok := buildPullRequestReviewCommentDocument(comment); ok {
			docs = append(docs, doc)
		}
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("repository_id = ?", repositoryID).Delete(&database.SearchDocument{}).Error; err != nil {
			return err
		}
		if len(docs) == 0 {
			return nil
		}
		return tx.CreateInBatches(docs, 200).Error
	})
}

func (s *Service) UpsertIssue(ctx context.Context, issue database.Issue) error {
	if issue.IsPullRequest {
		return s.DeleteByGitHubID(ctx, issue.RepositoryID, DocumentTypeIssue, issue.GitHubID)
	}
	doc, ok := buildIssueDocument(issue)
	if !ok {
		return nil
	}
	return s.upsertDocument(ctx, doc)
}

func (s *Service) UpsertPullRequest(ctx context.Context, pull database.PullRequest) error {
	doc, ok := buildPullRequestDocument(pull)
	if !ok {
		return nil
	}
	return s.upsertDocument(ctx, doc)
}

func (s *Service) UpsertIssueComment(ctx context.Context, comment database.IssueComment) error {
	doc, ok := buildIssueCommentDocument(comment)
	if !ok {
		return nil
	}
	return s.upsertDocument(ctx, doc)
}

func (s *Service) UpsertPullRequestReview(ctx context.Context, review database.PullRequestReview) error {
	doc, ok := buildPullRequestReviewDocument(review)
	if !ok {
		return nil
	}
	return s.upsertDocument(ctx, doc)
}

func (s *Service) UpsertPullRequestReviewComment(ctx context.Context, comment database.PullRequestReviewComment) error {
	doc, ok := buildPullRequestReviewCommentDocument(comment)
	if !ok {
		return nil
	}
	return s.upsertDocument(ctx, doc)
}

func (s *Service) DeleteByGitHubID(ctx context.Context, repositoryID uint, documentType string, githubID int64) error {
	return s.db.WithContext(ctx).
		Where("repository_id = ? AND document_type = ? AND document_github_id = ?", repositoryID, documentType, githubID).
		Delete(&database.SearchDocument{}).Error
}

func (s *Service) SearchMentions(ctx context.Context, repositoryID uint, request MentionRequest) ([]MentionMatch, error) {
	request, documentTypes, err := normalizeMentionRequest(request)
	if err != nil {
		return nil, err
	}
	if request.Mode == ModeFuzzy {
		return s.searchFallback(ctx, repositoryID, request, documentTypes)
	}
	if s.db.Dialector.Name() == "postgres" {
		return s.searchPostgres(ctx, repositoryID, request, documentTypes)
	}
	return s.searchFallback(ctx, repositoryID, request, documentTypes)
}

func (s *Service) searchPostgres(ctx context.Context, repositoryID uint, request MentionRequest, documentTypes []string) ([]MentionMatch, error) {
	query := s.baseQuery(ctx, repositoryID, request, documentTypes)
	switch request.Mode {
	case ModeFTS:
		query = query.
			Select("search_documents.*, ts_rank_cd(to_tsvector('simple', search_text), websearch_to_tsquery('simple', ?)) AS score", request.Query).
			Where("to_tsvector('simple', search_text) @@ websearch_to_tsquery('simple', ?)", request.Query).
			Order("score DESC").
			Order("object_updated_at DESC")
	case ModeRegex:
		if _, err := regexp.Compile(request.Query); err != nil {
			return nil, invalidMentionRequest("invalid regex")
		}
		query = query.
			Select("search_documents.*, 1.0 AS score").
			Where("(title_text ~* ? OR body_text ~* ?)", request.Query, request.Query).
			Order("object_updated_at DESC").
			Order("document_github_id ASC")
	default:
		return nil, invalidMentionRequest("unsupported mode")
	}

	var docs []scoredDocument
	if err := query.
		Limit(request.Limit).
		Offset((request.Page - 1) * request.Limit).
		Find(&docs).Error; err != nil {
		return nil, err
	}
	return buildMentionMatches(docs, request)
}

func (s *Service) searchFallback(ctx context.Context, repositoryID uint, request MentionRequest, documentTypes []string) ([]MentionMatch, error) {
	query := s.baseQuery(ctx, repositoryID, request, documentTypes).Order("object_updated_at DESC")
	var docs []database.SearchDocument
	if err := query.Find(&docs).Error; err != nil {
		return nil, err
	}

	re, err := compileRegexForMode(request.Mode, request.Query)
	if err != nil {
		return nil, err
	}

	scored := make([]scoredDocument, 0, len(docs))
	for _, doc := range docs {
		score := scoreDocument(doc, request, re)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredDocument{SearchDocument: doc, Score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].ObjectUpdatedAt.Equal(scored[j].ObjectUpdatedAt) {
				return scored[i].DocumentGitHubID < scored[j].DocumentGitHubID
			}
			return scored[i].ObjectUpdatedAt.After(scored[j].ObjectUpdatedAt)
		}
		return scored[i].Score > scored[j].Score
	})

	start := (request.Page - 1) * request.Limit
	if start >= len(scored) {
		return []MentionMatch{}, nil
	}
	end := start + request.Limit
	if end > len(scored) {
		end = len(scored)
	}
	return buildMentionMatches(scored[start:end], request)
}

func (s *Service) baseQuery(ctx context.Context, repositoryID uint, request MentionRequest, documentTypes []string) *gorm.DB {
	query := s.db.WithContext(ctx).Model(&database.SearchDocument{}).Where("repository_id = ?", repositoryID)
	if len(documentTypes) > 0 {
		query = query.Where("document_type IN ?", documentTypes)
	}
	if request.State != "" && request.State != "all" {
		query = query.Where("state = ?", request.State)
	}
	if strings.TrimSpace(request.Author) != "" {
		query = query.Where("LOWER(author_login) = ?", strings.ToLower(strings.TrimSpace(request.Author)))
	}
	return query
}

func (s *Service) upsertDocument(ctx context.Context, doc database.SearchDocument) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "repository_id"},
			{Name: "document_type"},
			{Name: "document_github_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"number",
			"state",
			"author_id",
			"author_login",
			"api_url",
			"html_url",
			"title_text",
			"body_text",
			"search_text",
			"normalized_text",
			"object_created_at",
			"object_updated_at",
			"updated_at",
		}),
	}).Create(&doc).Error
}

func normalizeMentionRequest(request MentionRequest) (MentionRequest, []string, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return MentionRequest{}, nil, invalidMentionRequest("query is required")
	}

	request.Mode = strings.TrimSpace(request.Mode)
	if request.Mode == "" {
		request.Mode = ModeFTS
	}
	switch request.Mode {
	case ModeFTS, ModeFuzzy, ModeRegex:
	default:
		return MentionRequest{}, nil, invalidMentionRequest("mode must be fts, fuzzy, or regex")
	}

	request.State = strings.TrimSpace(strings.ToLower(request.State))
	if request.State == "" {
		request.State = "all"
	}
	switch request.State {
	case "all", "open", "closed":
	default:
		return MentionRequest{}, nil, invalidMentionRequest("state must be open, closed, or all")
	}

	request.Author = strings.TrimSpace(request.Author)
	if request.Limit <= 0 {
		request.Limit = 20
	}
	if request.Limit > 100 {
		request.Limit = 100
	}
	if request.Page <= 0 {
		request.Page = 1
	}

	documentTypes, err := normalizeScopes(request.Scopes)
	if err != nil {
		return MentionRequest{}, nil, err
	}
	request.Scopes = scopesFromDocumentTypes(documentTypes)
	return request, documentTypes, nil
}

func normalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{
			DocumentTypeIssue,
			DocumentTypePullRequest,
			DocumentTypeIssueComment,
			DocumentTypePullRequestReview,
			DocumentTypePullRequestReviewComment,
		}, nil
	}

	documentTypes := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		var documentType string
		switch scope {
		case ScopeIssues:
			documentType = DocumentTypeIssue
		case ScopePullRequests:
			documentType = DocumentTypePullRequest
		case ScopeIssueComments:
			documentType = DocumentTypeIssueComment
		case ScopePullRequestReviews:
			documentType = DocumentTypePullRequestReview
		case ScopePullRequestReviewComments:
			documentType = DocumentTypePullRequestReviewComment
		default:
			return nil, invalidMentionRequest("unsupported scope")
		}
		if _, ok := seen[documentType]; ok {
			continue
		}
		seen[documentType] = struct{}{}
		documentTypes = append(documentTypes, documentType)
	}
	if len(documentTypes) == 0 {
		return nil, invalidMentionRequest("at least one valid scope is required")
	}
	return documentTypes, nil
}

func scopesFromDocumentTypes(documentTypes []string) []string {
	out := make([]string, 0, len(documentTypes))
	for _, documentType := range documentTypes {
		switch documentType {
		case DocumentTypeIssue:
			out = append(out, ScopeIssues)
		case DocumentTypePullRequest:
			out = append(out, ScopePullRequests)
		case DocumentTypeIssueComment:
			out = append(out, ScopeIssueComments)
		case DocumentTypePullRequestReview:
			out = append(out, ScopePullRequestReviews)
		case DocumentTypePullRequestReviewComment:
			out = append(out, ScopePullRequestReviewComments)
		}
	}
	return out
}

func invalidMentionRequest(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMentionRequest, message)
}

func buildMentionMatches(docs []scoredDocument, request MentionRequest) ([]MentionMatch, error) {
	re, err := compileRegexForMode(request.Mode, request.Query)
	if err != nil {
		return nil, err
	}
	results := make([]MentionMatch, 0, len(docs))
	for _, doc := range docs {
		field, excerpt := bestMatchField(doc.SearchDocument, request, re)
		results = append(results, MentionMatch{
			Resource: MentionResource{
				Type:    doc.DocumentType,
				ID:      doc.DocumentGitHubID,
				Number:  doc.Number,
				APIURL:  doc.APIURL,
				HTMLURL: doc.HTMLURL,
			},
			MatchedField: field,
			Excerpt:      excerpt,
			Score:        doc.Score,
		})
	}
	return results, nil
}

func compileRegexForMode(mode, query string) (*regexp.Regexp, error) {
	if mode != ModeRegex {
		return nil, nil
	}
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return nil, invalidMentionRequest("invalid regex")
	}
	return re, nil
}

func buildIssueDocument(issue database.Issue) (database.SearchDocument, bool) {
	if issue.IsPullRequest {
		return database.SearchDocument{}, false
	}
	return newSearchDocument(
		issue.RepositoryID,
		DocumentTypeIssue,
		issue.GitHubID,
		issue.Number,
		issue.State,
		issue.AuthorID,
		userLogin(issue.Author),
		issue.APIURL,
		issue.HTMLURL,
		issue.Title,
		issue.Body,
		issue.GitHubCreatedAt,
		issue.GitHubUpdatedAt,
	), true
}

func buildPullRequestDocument(pull database.PullRequest) (database.SearchDocument, bool) {
	if pull.IssueID == 0 {
		return database.SearchDocument{}, false
	}
	return newSearchDocument(
		pull.RepositoryID,
		DocumentTypePullRequest,
		pull.GitHubID,
		pull.Number,
		pull.State,
		pull.Issue.AuthorID,
		userLogin(pull.Issue.Author),
		pull.APIURL,
		pull.HTMLURL,
		pull.Issue.Title,
		pull.Issue.Body,
		pull.GitHubCreatedAt,
		pull.GitHubUpdatedAt,
	), true
}

func buildIssueCommentDocument(comment database.IssueComment) (database.SearchDocument, bool) {
	if comment.IssueID == 0 {
		return database.SearchDocument{}, false
	}
	return newSearchDocument(
		comment.RepositoryID,
		DocumentTypeIssueComment,
		comment.GitHubID,
		comment.Issue.Number,
		comment.Issue.State,
		comment.AuthorID,
		userLogin(comment.Author),
		comment.APIURL,
		comment.HTMLURL,
		"",
		comment.Body,
		comment.GitHubCreatedAt,
		comment.GitHubUpdatedAt,
	), true
}

func buildPullRequestReviewDocument(review database.PullRequestReview) (database.SearchDocument, bool) {
	if review.PullRequestID == 0 {
		return database.SearchDocument{}, false
	}
	return newSearchDocument(
		review.RepositoryID,
		DocumentTypePullRequestReview,
		review.GitHubID,
		review.PullRequest.Number,
		review.PullRequest.State,
		review.AuthorID,
		userLogin(review.Author),
		review.APIURL,
		review.HTMLURL,
		"",
		review.Body,
		review.GitHubCreatedAt,
		review.GitHubUpdatedAt,
	), true
}

func buildPullRequestReviewCommentDocument(comment database.PullRequestReviewComment) (database.SearchDocument, bool) {
	if comment.PullRequestID == 0 {
		return database.SearchDocument{}, false
	}
	return newSearchDocument(
		comment.RepositoryID,
		DocumentTypePullRequestReviewComment,
		comment.GitHubID,
		comment.PullRequest.Number,
		comment.PullRequest.State,
		comment.AuthorID,
		userLogin(comment.Author),
		comment.APIURL,
		comment.HTMLURL,
		"",
		comment.Body,
		comment.GitHubCreatedAt,
		comment.GitHubUpdatedAt,
	), true
}

func newSearchDocument(repositoryID uint, documentType string, githubID int64, number int, state string, authorID *uint, authorLogin, apiURL, htmlURL, titleText, bodyText string, createdAt, updatedAt time.Time) database.SearchDocument {
	searchText := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(titleText), strings.TrimSpace(bodyText)}, "\n\n"))
	return database.SearchDocument{
		RepositoryID:     repositoryID,
		DocumentType:     documentType,
		DocumentGitHubID: githubID,
		Number:           number,
		State:            strings.TrimSpace(strings.ToLower(state)),
		AuthorID:         authorID,
		AuthorLogin:      authorLogin,
		APIURL:           apiURL,
		HTMLURL:          htmlURL,
		TitleText:        strings.TrimSpace(titleText),
		BodyText:         strings.TrimSpace(bodyText),
		SearchText:       searchText,
		NormalizedText:   normalizeSearchText(searchText),
		ObjectCreatedAt:  createdAt.UTC(),
		ObjectUpdatedAt:  updatedAt.UTC(),
	}
}

func userLogin(user *database.User) string {
	if user == nil {
		return ""
	}
	return user.Login
}

func scoreDocument(doc database.SearchDocument, request MentionRequest, re *regexp.Regexp) float64 {
	titleScore := fieldScore(doc.TitleText, request, re)
	bodyScore := fieldScore(doc.BodyText, request, re)
	if titleScore > bodyScore {
		return titleScore
	}
	return bodyScore
}

func fieldScore(text string, request MentionRequest, re *regexp.Regexp) float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	switch request.Mode {
	case ModeFTS:
		return ftsFieldScore(text, request.Query)
	case ModeFuzzy:
		return fuzzyFieldScore(text, request.Query)
	case ModeRegex:
		if re != nil && re.MatchString(text) {
			return 1
		}
	}
	return 0
}

func bestMatchField(doc database.SearchDocument, request MentionRequest, re *regexp.Regexp) (string, string) {
	titleScore := fieldScore(doc.TitleText, request, re)
	bodyScore := fieldScore(doc.BodyText, request, re)
	field := "body"
	text := doc.BodyText
	if titleScore >= bodyScore && strings.TrimSpace(doc.TitleText) != "" {
		field = "title"
		text = doc.TitleText
	}
	if strings.TrimSpace(text) == "" {
		text = doc.SearchText
		if strings.TrimSpace(text) == "" {
			return field, ""
		}
	}
	return field, buildExcerpt(text, request, re)
}

func ftsFieldScore(text, query string) float64 {
	normalizedText := normalizeSearchText(text)
	normalizedQuery := normalizeSearchText(query)
	if normalizedText == "" || normalizedQuery == "" {
		return 0
	}
	score := 0.0
	if strings.Contains(normalizedText, normalizedQuery) {
		score += 2
	}
	tokens := strings.Fields(normalizedQuery)
	if len(tokens) == 0 {
		return score
	}
	matched := 0
	for _, token := range tokens {
		if strings.Contains(normalizedText, token) {
			matched++
		}
	}
	return score + float64(matched)/float64(len(tokens))
}

func fuzzyFieldScore(text, query string) float64 {
	normalizedText := normalizeSearchText(text)
	normalizedQuery := normalizeSearchText(query)
	if normalizedText == "" || normalizedQuery == "" {
		return 0
	}
	best := trigramSimilarity(normalizedQuery, normalizedText)
	textTokens := strings.Fields(normalizedText)
	queryTokens := strings.Fields(normalizedQuery)
	windowSize := len(queryTokens)
	if windowSize <= 0 {
		windowSize = 1
	}
	if len(textTokens) > 0 {
		minWindow := windowSize - 1
		if minWindow < 1 {
			minWindow = 1
		}
		maxWindow := windowSize + 2
		if maxWindow < 1 {
			maxWindow = 1
		}
		for size := minWindow; size <= maxWindow; size++ {
			if size > len(textTokens) {
				break
			}
			for i := 0; i+size <= len(textTokens); i++ {
				candidate := strings.Join(textTokens[i:i+size], " ")
				score := trigramSimilarity(normalizedQuery, candidate)
				if score > best {
					best = score
				}
			}
		}
	}
	return best
}

func buildExcerpt(text string, request MentionRequest, re *regexp.Regexp) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	start := 0

	switch request.Mode {
	case ModeRegex:
		if re != nil {
			if loc := re.FindStringIndex(text); loc != nil {
				start = len([]rune(text[:loc[0]]))
			}
		}
	case ModeFTS, ModeFuzzy:
		needle := strings.TrimSpace(request.Query)
		if needle != "" {
			lowerText := strings.ToLower(text)
			lowerNeedle := strings.ToLower(needle)
			if idx := strings.Index(lowerText, lowerNeedle); idx >= 0 {
				start = len([]rune(text[:idx]))
			} else {
				for _, token := range strings.Fields(normalizeSearchText(needle)) {
					if token == "" {
						continue
					}
					if idx := strings.Index(lowerText, token); idx >= 0 {
						start = len([]rune(text[:idx]))
						break
					}
				}
			}
		}
	}

	const excerptLength = 160
	if len(runes) <= excerptLength {
		return text
	}
	if start > 50 {
		start -= 50
	} else {
		start = 0
	}
	end := start + excerptLength
	if end > len(runes) {
		end = len(runes)
	}
	excerpt := string(runes[start:end])
	if start > 0 {
		excerpt = "..." + excerpt
	}
	if end < len(runes) {
		excerpt += "..."
	}
	return excerpt
}

func normalizeSearchText(text string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func trigramSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	aSet := trigramSet(a)
	bSet := trigramSet(b)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}
	shared := 0
	for token := range aSet {
		if _, ok := bSet[token]; ok {
			shared++
		}
	}
	return (2 * float64(shared)) / float64(len(aSet)+len(bSet))
}

func trigramSet(text string) map[string]struct{} {
	text = "  " + text + "  "
	runes := []rune(text)
	out := make(map[string]struct{}, len(runes))
	if len(runes) < 3 {
		return out
	}
	for i := 0; i+3 <= len(runes); i++ {
		out[string(runes[i:i+3])] = struct{}{}
	}
	return out
}

func fuzzyThreshold(normalized string) float64 {
	switch {
	case len(normalized) <= 4:
		return 0.55
	case len(normalized) <= 8:
		return 0.4
	default:
		return 0.28
	}
}
