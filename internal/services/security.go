package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"inventory/internal/models"
)

// 安全问题与答案的长度限制（按字符数，而非字节数）。
const (
	// SecurityQuestionMinLen 问题文本最短长度
	SecurityQuestionMinLen = 4
	// SecurityQuestionMaxLen 问题文本最长长度
	SecurityQuestionMaxLen = 100
	// SecurityAnswerMinLen 答案最短长度
	SecurityAnswerMinLen = 2
	// SecurityAnswerMaxLen 答案最长长度
	SecurityAnswerMaxLen = 100
)

// NormalizeAnswer 归一化安全问题的答案。
//
// 规则：去除首尾空白、把内部连续空白折叠为单个空格、统一转小写。
// 这样用户下次输入时不会因为大小写或多余空格而被判错。
func NormalizeAnswer(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// ValidateSecurityAnswers 校验一组安全问题的输入。
// 返回的错误已带 ErrInvalidInput 包装，可直接展示给用户。
func ValidateSecurityAnswers(inputs []models.AnswerInput) error {
	if len(inputs) != models.SecurityQuestionCount {
		return fmt.Errorf("%w：需要设置 %d 个安全问题", ErrInvalidInput, models.SecurityQuestionCount)
	}

	seen := make(map[string]struct{}, len(inputs))
	for i, in := range inputs {
		no := i + 1
		question := strings.TrimSpace(in.Question)
		answer := strings.TrimSpace(in.Answer)

		qLen := utf8.RuneCountInString(question)
		if qLen < SecurityQuestionMinLen {
			return fmt.Errorf("%w：第 %d 个问题至少需要 %d 个字", ErrInvalidInput, no, SecurityQuestionMinLen)
		}
		if qLen > SecurityQuestionMaxLen {
			return fmt.Errorf("%w：第 %d 个问题不能超过 %d 个字", ErrInvalidInput, no, SecurityQuestionMaxLen)
		}

		aLen := utf8.RuneCountInString(answer)
		if aLen < SecurityAnswerMinLen {
			return fmt.Errorf("%w：第 %d 个问题的答案至少需要 %d 个字", ErrInvalidInput, no, SecurityAnswerMinLen)
		}
		if aLen > SecurityAnswerMaxLen {
			return fmt.Errorf("%w：第 %d 个问题的答案不能超过 %d 个字", ErrInvalidInput, no, SecurityAnswerMaxLen)
		}

		key := strings.ToLower(question)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w：第 %d 个问题与前面的问题重复了", ErrInvalidInput, no)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// SetSecurityQuestions 替换用户的全部安全问题（先清空再写入，整体在一个事务内）。
func (s *Store) SetSecurityQuestions(ctx context.Context, userID int64, inputs []models.AnswerInput) error {
	if err := ValidateSecurityAnswers(inputs); err != nil {
		return err
	}

	hashes := make([]string, 0, len(inputs))
	for _, in := range inputs {
		hash, err := HashPassword(NormalizeAnswer(in.Answer))
		if err != nil {
			return err
		}
		hashes = append(hashes, hash)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM security_questions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("清理旧安全问题失败: %w", err)
	}

	now := nowUTC()
	for i, in := range inputs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO security_questions (user_id, position, question, answer_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			userID, i+1, strings.TrimSpace(in.Question), hashes[i], now, now); err != nil {
			return fmt.Errorf("写入安全问题失败: %w", err)
		}
	}

	return tx.Commit()
}

// GetSecurityQuestions 返回用户已设置的安全问题，按题号排序。
// 返回值包含答案摘要，仅供服务端校验使用，不要直接渲染到页面。
func (s *Store) GetSecurityQuestions(ctx context.Context, userID int64) ([]models.SecurityQuestion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, position, question, answer_hash, created_at, updated_at
		FROM security_questions WHERE user_id = ? ORDER BY position ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询安全问题失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.SecurityQuestion, 0, models.SecurityQuestionCount)
	for rows.Next() {
		var (
			q                  models.SecurityQuestion
			createdAt, updated dbTime
		)
		if err := rows.Scan(&q.ID, &q.UserID, &q.Position, &q.Question, &q.AnswerHash,
			&createdAt, &updated); err != nil {
			return nil, fmt.Errorf("解析安全问题失败: %w", err)
		}
		q.CreatedAt = createdAt.Time
		q.UpdatedAt = updated.Time
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSecurityQuestionTexts 只返回问题文本，供「找回密码」页面展示。
func (s *Store) GetSecurityQuestionTexts(ctx context.Context, userID int64) ([]string, error) {
	questions, err := s.GetSecurityQuestions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(questions))
	for _, q := range questions {
		out = append(out, q.Question)
	}
	return out, nil
}

// CountSecurityQuestions 返回用户已设置的安全问题数量。
func (s *Store) CountSecurityQuestions(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM security_questions WHERE user_id = ?`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计安全问题数量失败: %w", err)
	}
	return n, nil
}

// HasSecurityQuestions 判断用户是否已完整设置安全问题。
func (s *Store) HasSecurityQuestions(ctx context.Context, userID int64) (bool, error) {
	n, err := s.CountSecurityQuestions(ctx, userID)
	if err != nil {
		return false, err
	}
	return n >= models.SecurityQuestionCount, nil
}

// VerifySecurityAnswers 校验用户提交的答案是否全部正确。
//
// answers 按题号顺序传入。为了让耗时与答对数量无关，这里会校验全部题目，
// 而不是遇到第一个错误就返回。
func (s *Store) VerifySecurityAnswers(ctx context.Context, userID int64, answers []string) (bool, error) {
	questions, err := s.GetSecurityQuestions(ctx, userID)
	if err != nil {
		return false, err
	}
	if len(questions) < models.SecurityQuestionCount {
		return false, ErrSecurityQuestionsNotSet
	}
	if len(answers) != len(questions) {
		return false, nil
	}

	allOK := true
	for i, q := range questions {
		// 不要短路：即使前面已经错了也要继续比对，避免通过响应时间推测答对了几题
		if !VerifyPassword(q.AnswerHash, NormalizeAnswer(answers[i])) {
			allOK = false
		}
	}
	return allOK, nil
}

// ClearSecurityQuestions 清空用户的安全问题。
func (s *Store) ClearSecurityQuestions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM security_questions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("清除安全问题失败: %w", err)
	}
	return nil
}

// ErrSecurityQuestionsNotSetOf 判断错误是否为「未设置安全问题」。
func ErrSecurityQuestionsNotSetOf(err error) bool {
	return errors.Is(err, ErrSecurityQuestionsNotSet) || errors.Is(err, sql.ErrNoRows)
}
