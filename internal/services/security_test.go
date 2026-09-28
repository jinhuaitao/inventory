package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"inventory/internal/models"
)

// validAnswers 返回一组合法的安全问题输入。
func validAnswers() []models.AnswerInput {
	return []models.AnswerInput{
		{Question: "我小学班主任的姓名是？", Answer: "张老师"},
		{Question: "我第一辆自行车的颜色？", Answer: "Blue"},
		{Question: "我母亲的出生城市是？", Answer: "杭州"},
	}
}

func TestNormalizeAnswer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"去首尾空格", "  Blue  ", "blue"},
		{"转小写", "BLUE", "blue"},
		{"折叠内部连续空白", "Zhang   San", "zhang san"},
		{"制表符与换行", "a\t\nb", "a b"},
		{"中文不受大小写影响", " 张老师 ", "张老师"},
		{"空字符串", "   ", ""},
		{"全角空格", "　Blue　", "blue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeAnswer(tc.in); got != tc.want {
				t.Errorf("NormalizeAnswer(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateSecurityAnswers(t *testing.T) {
	t.Run("合法输入通过", func(t *testing.T) {
		if err := ValidateSecurityAnswers(validAnswers()); err != nil {
			t.Fatalf("合法输入不应报错: %v", err)
		}
	})

	t.Run("数量不足被拒绝", func(t *testing.T) {
		in := validAnswers()[:2]
		err := ValidateSecurityAnswers(in)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
	})

	t.Run("问题过短被拒绝", func(t *testing.T) {
		in := validAnswers()
		in[1].Question = "短"
		if err := ValidateSecurityAnswers(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
	})

	t.Run("答案过短被拒绝", func(t *testing.T) {
		in := validAnswers()
		in[2].Answer = "x"
		if err := ValidateSecurityAnswers(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
	})

	t.Run("问题重复被拒绝", func(t *testing.T) {
		in := validAnswers()
		in[2].Question = in[0].Question
		err := ValidateSecurityAnswers(in)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
		if !strings.Contains(err.Error(), "重复") {
			t.Errorf("错误信息应说明重复，实际 %q", err.Error())
		}
	})

	t.Run("问题仅大小写不同也算重复", func(t *testing.T) {
		in := validAnswers()
		in[0].Question = "My First School"
		in[1].Question = "my first school"
		if err := ValidateSecurityAnswers(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
	})

	t.Run("超长问题被拒绝", func(t *testing.T) {
		in := validAnswers()
		in[0].Question = strings.Repeat("长", SecurityQuestionMaxLen+1)
		if err := ValidateSecurityAnswers(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("期望 ErrInvalidInput，实际 %v", err)
		}
	})
}

func TestSecurityQuestionsLifecycle(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "erin", Email: "erin@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	// 初始未设置
	if has, err := store.HasSecurityQuestions(ctx, user.ID); err != nil || has {
		t.Fatalf("初始不应有安全问题，has=%v err=%v", has, err)
	}

	// 设置成功
	if err := store.SetSecurityQuestions(ctx, user.ID, validAnswers()); err != nil {
		t.Fatalf("设置安全问题失败: %v", err)
	}

	count, err := store.CountSecurityQuestions(ctx, user.ID)
	if err != nil {
		t.Fatalf("统计安全问题失败: %v", err)
	}
	if count != models.SecurityQuestionCount {
		t.Fatalf("安全问题数量 = %d, 期望 %d", count, models.SecurityQuestionCount)
	}

	if has, err := store.HasSecurityQuestions(ctx, user.ID); err != nil || !has {
		t.Fatalf("设置后应返回 true，has=%v err=%v", has, err)
	}

	// 问题文本可读出，且按题号排序
	texts, err := store.GetSecurityQuestionTexts(ctx, user.ID)
	if err != nil {
		t.Fatalf("读取问题文本失败: %v", err)
	}
	if len(texts) != 3 {
		t.Fatalf("问题数量 = %d, 期望 3", len(texts))
	}
	if texts[0] != "我小学班主任的姓名是？" {
		t.Errorf("第一个问题 = %q", texts[0])
	}

	// 答案摘要不应为空
	questions, err := store.GetSecurityQuestions(ctx, user.ID)
	if err != nil {
		t.Fatalf("读取安全问题失败: %v", err)
	}
	for _, q := range questions {
		if q.AnswerHash == "" {
			t.Errorf("第 %d 题的答案摘要为空", q.Position)
		}
		if strings.Contains(q.AnswerHash, "张老师") || strings.Contains(q.AnswerHash, "杭州") {
			t.Errorf("第 %d 题的答案不应以明文存储", q.Position)
		}
	}
}

func TestVerifySecurityAnswers(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "henry", Email: "henry@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	if err := store.SetSecurityQuestions(ctx, user.ID, validAnswers()); err != nil {
		t.Fatalf("设置安全问题失败: %v", err)
	}

	t.Run("全部正确", func(t *testing.T) {
		ok, err := store.VerifySecurityAnswers(ctx, user.ID, []string{"张老师", "Blue", "杭州"})
		if err != nil {
			t.Fatalf("校验出错: %v", err)
		}
		if !ok {
			t.Error("正确答案应通过校验")
		}
	})

	t.Run("大小写与空格不影响", func(t *testing.T) {
		ok, err := store.VerifySecurityAnswers(ctx, user.ID, []string{" 张老师 ", "BLUE", "杭州"})
		if err != nil {
			t.Fatalf("校验出错: %v", err)
		}
		if !ok {
			t.Error("大小写与空格归一化后应通过校验")
		}
	})

	t.Run("部分错误不通过", func(t *testing.T) {
		ok, err := store.VerifySecurityAnswers(ctx, user.ID, []string{"张老师", "Red", "杭州"})
		if err != nil {
			t.Fatalf("校验出错: %v", err)
		}
		if ok {
			t.Error("存在错误答案时不应通过")
		}
	})

	t.Run("全部错误不通过", func(t *testing.T) {
		ok, err := store.VerifySecurityAnswers(ctx, user.ID, []string{"a1", "b2", "c3"})
		if err != nil {
			t.Fatalf("校验出错: %v", err)
		}
		if ok {
			t.Error("全错时不应通过")
		}
	})

	t.Run("答案数量不匹配不通过", func(t *testing.T) {
		ok, err := store.VerifySecurityAnswers(ctx, user.ID, []string{"张老师"})
		if err != nil {
			t.Fatalf("校验出错: %v", err)
		}
		if ok {
			t.Error("答案数量不匹配时不应通过")
		}
	})

	t.Run("未设置安全问题时返回明确错误", func(t *testing.T) {
		other, err := store.CreateUser(ctx, CreateUserInput{
			Username: "iris", Email: "iris@example.com",
			Password: "Secret@123", Role: models.RoleViewer,
		})
		if err != nil {
			t.Fatalf("创建用户失败: %v", err)
		}
		_, err = store.VerifySecurityAnswers(ctx, other.ID, []string{"a1", "b2", "c3"})
		if !errors.Is(err, ErrSecurityQuestionsNotSet) {
			t.Fatalf("期望 ErrSecurityQuestionsNotSet，实际 %v", err)
		}
	})
}

func TestSetSecurityQuestionsReplacesOld(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "jack", Email: "jack@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	if err := store.SetSecurityQuestions(ctx, user.ID, validAnswers()); err != nil {
		t.Fatalf("首次设置失败: %v", err)
	}

	// 重新设置应整体替换，而不是追加
	updated := []models.AnswerInput{
		{Question: "我最喜欢的城市是？", Answer: "成都"},
		{Question: "我最喜欢的动物是？", Answer: "橘猫"},
		{Question: "我第一份工作的公司是？", Answer: "示例科技"},
	}
	if err := store.SetSecurityQuestions(ctx, user.ID, updated); err != nil {
		t.Fatalf("重新设置失败: %v", err)
	}

	count, err := store.CountSecurityQuestions(ctx, user.ID)
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 3 {
		t.Fatalf("重新设置后数量 = %d, 期望 3（不应追加）", count)
	}

	texts, err := store.GetSecurityQuestionTexts(ctx, user.ID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if texts[0] != "我最喜欢的城市是？" {
		t.Errorf("重新设置后第一个问题 = %q", texts[0])
	}

	// 旧答案失效，新答案生效
	if ok, _ := store.VerifySecurityAnswers(ctx, user.ID, []string{"张老师", "Blue", "杭州"}); ok {
		t.Error("旧答案在重新设置后不应再通过")
	}
	if ok, _ := store.VerifySecurityAnswers(ctx, user.ID, []string{"成都", "橘猫", "示例科技"}); !ok {
		t.Error("新答案应通过校验")
	}
}

func TestSetSecurityQuestionsRejectsInvalid(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "kate", Email: "kate@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	// 问题文本过短：答案合法，用于确认失败原因确实来自问题本身
	bad := []models.AnswerInput{
		{Question: "短", Answer: "答案甲"},
		{Question: "另一个有效问题", Answer: "答案乙"},
		{Question: "第三个有效问题", Answer: "答案丙"},
	}
	if err := store.SetSecurityQuestions(ctx, user.ID, bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("非法输入应返回 ErrInvalidInput，实际 %v", err)
	}

	// 校验失败时不应写入任何记录
	if n, err := store.CountSecurityQuestions(ctx, user.ID); err != nil || n != 0 {
		t.Fatalf("校验失败后不应写入数据，实际 %d 条", n)
	}
}

func TestClearSecurityQuestions(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "leo", Email: "leo@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	if err := store.SetSecurityQuestions(ctx, user.ID, validAnswers()); err != nil {
		t.Fatalf("设置安全问题失败: %v", err)
	}

	if err := store.ClearSecurityQuestions(ctx, user.ID); err != nil {
		t.Fatalf("清除失败: %v", err)
	}
	if n, err := store.CountSecurityQuestions(ctx, user.ID); err != nil || n != 0 {
		t.Fatalf("清除后应为 0，实际 %d", n)
	}
}

func TestDeleteUserCascadesSecurityQuestions(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "mona", Email: "mona@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	if err := store.SetSecurityQuestions(ctx, user.ID, validAnswers()); err != nil {
		t.Fatalf("设置安全问题失败: %v", err)
	}

	if err := store.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("删除用户失败: %v", err)
	}

	var n int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM security_questions WHERE user_id = ?`, user.ID).Scan(&n); err != nil {
		t.Fatalf("查询残留记录失败: %v", err)
	}
	if n != 0 {
		t.Errorf("删除用户后应级联清除安全问题，残留 %d 条", n)
	}
}
