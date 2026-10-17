// Package token —— 会话令牌吊销黑名单(gopherforge JWT 黑名单模式)。
//
// 经 Redis 记录已登出令牌的 jti,键 opic:techbase:jwt:blacklist:<jti>,TTL=令牌剩余有效期。
// 失败闭合(2026-10 审计修复):吊销通道不可用时不再静默放行——
//   - 写入侧 Revoke 在 Redis 未启用/写入失败时记入进程内兜底黑名单并返回错误(可观测),不再空操作;
//   - 判读侧 Revoked 在 Redis 未启用/查询失败时按"已吊销"拒绝,不再"可用性优先"放行。
//
// 部署前提:本地会话(HS256)要求 REDIS_ENABLED=true;进程内兜底不跨副本(详见补丁 NOTES)。
package token

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"gitcode.com/opic-ontology/opic-techbase/pkg/redisx"
)

const keyPrefix = "opic:techbase:jwt:blacklist:"

// localFallback 进程内兜底黑名单(jti → 过期时刻):Redis 未启用或写入失败时记录,
// 判读侧优先命中。多副本不共享、进程重启即失效(仅兜底,主通道仍是 Redis)。
var localFallback = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

// localStore 记入进程内兜底黑名单。
func localStore(jti string, ttl time.Duration) {
	localFallback.Lock()
	defer localFallback.Unlock()
	localFallback.m[jti] = time.Now().Add(ttl)
}

// localHit 是否命中进程内兜底(顺带清理过期项)。
func localHit(jti string) bool {
	localFallback.Lock()
	defer localFallback.Unlock()
	exp, ok := localFallback.m[jti]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(localFallback.m, jti)
		return false
	}
	return true
}

// Revoke 吊销令牌(jti 黑名单,TTL 至令牌自然过期)。
// 失败闭合:Redis 未启用或写入失败时记入进程内兜底并返回错误,绝不静默空操作。
func Revoke(jti string, ttl time.Duration) error {
	if jti == "" {
		return nil
	}
	if !redisx.Enabled() {
		localStore(jti, ttl)
		log.Printf("[token] redis 未启用,吊销落进程内兜底(不跨副本)")
		return fmt.Errorf("令牌吊销未写入 redis(已落进程内兜底)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := redisx.Client.Set(ctx, keyPrefix+jti, 1, ttl).Err(); err != nil {
		localStore(jti, ttl)
		log.Printf("[token] redis 写入失败,吊销落进程内兜底: %v", err)
		return fmt.Errorf("令牌吊销写入 redis 失败(已落进程内兜底): %w", err)
	}
	return nil
}

// Revoked 是否已被吊销。失败闭合:吊销通道不可核验(未启用/查询失败)时按已吊销拒绝,
// 使登出与停权在依赖故障窗口内即时收敛(可用性让位于安全)。
func Revoked(jti string) bool {
	if jti == "" {
		return true // 无 jti 无法核验 → 失败闭合
	}
	if localHit(jti) {
		return true
	}
	if !redisx.Enabled() {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := redisx.Client.Exists(ctx, keyPrefix+jti).Result()
	if err != nil {
		// Redis 故障:不再按未吊销放行(fail-closed)
		return true
	}
	return n > 0
}
