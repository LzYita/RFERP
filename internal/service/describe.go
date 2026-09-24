package service

import (
	"app/internal/config"
	"app/internal/usecase"
)

// Describe 报告当前绑定的数据库身份（D-016）。
//
// 本机模式下即本机库；它是「接入服务器」预检的一半输入：
// 另一半来自目标服务器的 /api/v1/serverinfo，两者由 usecase.PlanLink 比较。
//
// AppVersion 由进程侧填写（服务端在 handler 里补），这里只报数据库事实。
func (s *Service) Describe() (usecase.ServerInfo, error) {
	id, err := s.repo.GetDatabaseID()
	if err != nil {
		return usecase.ServerInfo{}, err
	}
	ver, err := s.repo.GetSchemaVersion()
	if err != nil {
		return usecase.ServerInfo{}, err
	}
	storage := config.StorageMySQL
	if s.cfg != nil {
		storage = s.cfg.StorageKind()
	}
	return usecase.ServerInfo{
		APIVersion:    usecase.APIVersion,
		SchemaVersion: ver,
		DatabaseID:    id,
		Storage:       storage,
	}, nil
}

var _ usecase.ServerDescriptor = (*Service)(nil)
