export interface RemoteState { language?: "en"|"zh"; path: string | ((lang:"en"|"zh")=>string); leave: () => void; closedAt?: number; refused?: boolean; retryAt?: number }
