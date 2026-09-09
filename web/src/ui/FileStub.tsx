// L3 replaces this import with ./FileChip; its public props are already wired into the shell.
import type { AttachedFile } from '../attachments';
export function FileChip(_props: { file: AttachedFile; reading?: boolean; onRemove?: () => void; onOpen?: () => void }) { return null; }
export function FileSheet(_props: { file: AttachedFile; host: string; onClose: () => void }) { return null; }
