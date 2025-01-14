package impl

import (
	"context"
	"time"

	"github.com/infraboard/mcenter/apps/code"
	"github.com/infraboard/mcenter/apps/namespace"
	"github.com/infraboard/mcenter/apps/policy"
	"github.com/infraboard/mcenter/apps/token"
	"github.com/infraboard/mcenter/apps/token/provider"
	"github.com/infraboard/mcenter/apps/user"
	"github.com/infraboard/mcube/exception"
	"github.com/infraboard/mcube/http/request"
)

func (s *service) IssueToken(ctx context.Context, req *token.IssueTokenRequest) (
	*token.Token, error) {
	// 登陆前安全检查
	if err := s.BeforeLoginSecurityCheck(ctx, req); err != nil {
		return nil, exception.NewBadRequest(err.Error())
	}

	// 颁发令牌
	tk, err := s.IssueTokenNow(ctx, req)
	if err != nil {
		return nil, err
	}

	// 登陆后安全检查
	if err := s.AfterLoginSecurityCheck(ctx, req.VerifyCode, tk); err != nil {
		return nil, exception.NewBadRequest(err.Error())
	}

	// 还原用户上次登陆状态(上次登陆的空间)
	err = s.RestoreUserState(ctx, tk)
	if err != nil {
		return nil, err
	}

	return tk, nil
}

func (s *service) IssueTokenNow(ctx context.Context, req *token.IssueTokenRequest) (*token.Token, error) {
	// 获取令牌颁发器
	issuer := provider.Get(token.GRANT_TYPE_PASSWORD)

	// 确保有provider
	if issuer == nil {
		return nil, exception.NewBadRequest("grant type %s not support", req.GrantType)
	}

	// 颁发token
	tk, err := issuer.IssueToken(ctx, req)
	if err != nil {
		return nil, err
	}

	if !req.DryRun {
		// 入库保存
		if err := s.save(ctx, tk); err != nil {
			return nil, err
		}

		// 关闭之前的web登录
		if err := s.blockOtherWebToken(ctx, tk); err != nil {
			return nil, err
		}
	}

	return tk, nil
}

func (s *service) BeforeLoginSecurityCheck(ctx context.Context, req *token.IssueTokenRequest) error {
	// 连续登录失败检测
	if err := s.checker.MaxFailedRetryCheck(ctx, req); err != nil {
		return exception.NewBadRequest("%s", err)
	}

	// IP保护检测
	err := s.checker.IPProtectCheck(ctx, req)
	if err != nil {
		return err
	}

	s.log.Debug("security check complete")
	return nil
}

func (s *service) AfterLoginSecurityCheck(ctx context.Context, verifyCode string, tk *token.Token) error {
	// 如果有校验码, 则直接通过校验码检测用户身份安全
	if verifyCode != "" {
		s.log.Debugf("verify code provided, check code ...")
		_, err := s.code.VerifyCode(ctx, code.NewVerifyCodeRequest(tk.Username, verifyCode))
		if err != nil {
			return exception.NewPermissionDeny("verify code invalidate, error, %s", err)
		}
		s.log.Debugf("verfiy code check passed")
		return nil
	}

	// 异地登录检测
	err := s.checker.OtherPlaceLoggedInChecK(ctx, tk)
	if err != nil {
		return exception.NewVerifyCodeRequiredError("异地登录检测失败: %s", err)
	}

	// 长时间未登录检测
	err = s.checker.NotLoginDaysChecK(ctx, tk)
	if err != nil {
		return exception.NewVerifyCodeRequiredError("长时间未登录检测失败: %s", err)
	}

	return nil
}

func (s *service) RestoreUserState(ctx context.Context, tk *token.Token) error {
	qt := token.NewQueryTokenRequest()
	qt.Page.PageSize = 1
	qt.Platform = token.NewPlatform(token.PLATFORM_WEB)
	qt.UserId = tk.UserId
	set, err := s.QueryToken(ctx, qt)
	if err != nil {
		return err
	}
	if set.Length() == 0 {
		return nil
	}

	latestTK := set.Items[0]
	tk.Namespace = latestTK.Namespace

	return nil
}

// 撤销Token
func (s *service) RevolkToken(ctx context.Context, req *token.RevolkTokenRequest) (
	*token.Token, error) {
	tk, err := s.get(ctx, req.AccessToken)
	if err != nil {
		return nil, err
	}

	if tk.RefreshToken != req.RefreshToken {
		return nil, exception.NewBadRequest("refresh token not connrect")
	}

	if err := s.delete(ctx, tk); err != nil {
		return nil, err
	}
	return tk, nil
}

// 切换Token空间
func (s *service) ChangeNamespace(ctx context.Context, req *token.ChangeNamespaceRequest) (
	*token.Token, error) {
	if err := req.Validate(); err != nil {
		return nil, exception.NewBadRequest("validate change namespace error, %s", err)
	}

	tk, err := s.DescribeToken(ctx, token.NewDescribeTokenRequest(req.Token))
	if err != nil {
		return nil, err
	}

	_, err = s.ns.DescribeNamespace(ctx, namespace.NewDescriptNamespaceRequest(tk.Domain, req.Namespace))
	if err != nil {
		return nil, err
	}

	if !tk.UserType.IsIn(user.TYPE_PRIMARY, user.TYPE_SUPPER) && !tk.HasNamespace(req.Namespace) {
		return nil, exception.NewPermissionDeny("your has no permission to access namespace %s", req.Namespace)
	}

	tk.Namespace = req.Namespace
	if err := s.update(ctx, tk); err != nil {
		return nil, err
	}

	return tk, nil
}

// 校验Token
func (s *service) ValidateToken(ctx context.Context, req *token.ValidateTokenRequest) (
	*token.Token, error) {
	if err := req.Validate(); err != nil {
		return nil, exception.NewBadRequest(err.Error())
	}

	tk, err := s.get(ctx, req.AccessToken)
	if err != nil {
		return nil, exception.NewUnauthorized(err.Error())
	}

	if tk.Status.IsBlock {
		return nil, s.makeBlockExcption(tk.Status.BlockType, tk.Status.BlockMessage())
	}

	// 校验Access Token是否过期
	if tk.CheckAccessIsExpired() {
		// 如果Refresh还没有过期, 自动再续一个周期, 避免用户连续使用过程中导致访问中断
		if err := s.reuseToken(ctx, tk); err != nil {
			return nil, err
		}
	}

	return tk, nil
}

func (s *service) makeBlockExcption(bt token.BLOCK_TYPE, message string) exception.APIException {
	switch bt {
	case token.BLOCK_TYPE_REFRESH_TOKEN_EXPIRED:
		return exception.NewSessionTerminated(message)
	case token.BLOCK_TYPE_OTHER_PLACE_LOGGED_IN:
		return exception.NewOtherPlaceLoggedIn(message)
	case token.BLOCK_TYPE_OTHER_IP_LOGGED_IN:
		return exception.NewOtherIPLoggedIn(message)
	default:
		return exception.NewInternalServerError("unknow block type: %s, message: %s", bt, message)
	}
}

func (s *service) reuseToken(ctx context.Context, tk *token.Token) error {
	// 刷新token过期的，不允许复用
	if tk.CheckRefreshIsExpired() {
		return exception.NewRefreshTokenExpired("refresh_token: %s expoired", tk.RefreshToken)
	}

	// access token延长一个过期周期
	tk.AccessExpiredAt = time.Now().Add(time.Duration(token.DEFAULT_ACCESS_TOKEN_EXPIRE_SECOND)*time.Second).Unix() * 1000
	// refresh token延长一个过期周期
	tk.RefreshExpiredAt = time.Unix(tk.RefreshExpiredAt/1000, 0).Add(time.Duration(token.DEFAULT_REFRESH_TOKEN_EXPIRE_SECOND)*time.Second).Unix() * 1000
	return s.save(ctx, tk)
}

// 查询Token, 用于查询Token颁发记录, 也就是登陆日志
func (s *service) QueryToken(ctx context.Context, req *token.QueryTokenRequest) (*token.TokenSet, error) {
	query := newQueryRequest(req)
	resp, err := s.col.Find(ctx, query.FindFilter(), query.FindOptions())

	if err != nil {
		return nil, exception.NewInternalServerError("find token error, error is %s", err)
	}

	tokenSet := token.NewTokenSet()
	// 循环
	for resp.Next(ctx) {
		tk := new(token.Token)
		if err := resp.Decode(tk); err != nil {
			return nil, exception.NewInternalServerError("decode token error, error is %s", err)
		}
		tokenSet.Add(tk)
	}

	// count
	count, err := s.col.CountDocuments(ctx, query.FindFilter())
	if err != nil {
		return nil, exception.NewInternalServerError("get token count error, error is %s", err)
	}
	tokenSet.Total = count

	return tokenSet, nil
}

func (s *service) DescribeToken(ctx context.Context, req *token.DescribeTokenRequest) (*token.Token, error) {
	if err := req.Validate(); err != nil {
		return nil, exception.NewBadRequest(err.Error())
	}

	tk, err := s.get(ctx, req.DescribeValue)
	if err != nil {
		return nil, exception.NewUnauthorized(err.Error())
	}

	// 查询用户可以访问的空间
	query := policy.NewQueryPolicyRequest()
	query.Page = request.NewPageRequest(policy.MAX_USER_POLICY, 1)
	query.Username = tk.Username
	ps, err := s.policy.QueryPolicy(ctx, query)
	if err != nil {
		return nil, err
	}
	if ps.Total > policy.MAX_USER_POLICY {
		s.log.Warnf("user policy large than max policy count %d, total: %d", policy.MAX_USER_POLICY, ps.Total)
	}
	tk.AvailableNamespace = ps.GetNamespace()
	return tk, nil
}
