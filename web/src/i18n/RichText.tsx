import { Fragment, type ReactNode } from 'react';
import { tr, type AppKey } from './text';

/** Named rich values preserve code/emphasis while allowing Chinese to reorder the sentence. */
export function Text({ name, values }: { name: AppKey; values: Record<string, ReactNode> }) {
  return <>{tr(name).split(/(\{\w+\})/).map((part, i) =>
    <Fragment key={i}>{/^\{\w+\}$/.test(part) ? values[part.slice(1, -1)] : part}</Fragment>)}</>;
}
