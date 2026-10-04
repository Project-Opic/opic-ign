/** AI 智能助理 API（O-IGN 语境：多模型选择/参数档位 + 只读 SQL 工具） */
import { http } from './request'

/** 只读 SQL 查询结果(渲染协议 table) */
export interface AssistantTable {
  columns: string[]
  rows: Record<string, unknown>[]
  count: number
  sql: string
}

export interface AssistantReply {
  answer: string
  hints: string[]
  action?: string
}

export interface ModelOption {
  account_id: number
  account_name: string
  provider: string
  models: string[]
  default_model: string
}

export const assistantApi = {
  chat: (question: string, accountId?: number, model?: string, temperature?: number) =>
    http.post<AssistantReply>('/api/assistant/chat', {
      question,
      account_id: accountId || 0,
      model: model || '',
      temperature: temperature ?? 0,
    }),
  models: () => http.get<{ models: ModelOption[] }>('/api/assistant/models'),
  /** 只读 SQL 工具(仅 SELECT/WITH + ign 白名单 + 强制 LIMIT + 审计) */
  sql: (sqlText: string) => http.post<AssistantTable>('/api/assistant/query', { sql: sqlText }),
}
