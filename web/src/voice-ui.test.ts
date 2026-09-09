import { describe, expect, it, vi } from 'vitest';
import { createElement, type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import MessageView from './ui/Message';
import { GatewayError } from './api';

const props = (): ComponentProps<typeof MessageView> => ({
  message: { id: 'reply', role: 'assistant', content: '**Hello** from the host.', status: 'complete' },
  host: 'Test host', live: false, busy: false, answering: false, undelivered: false, carried: false,
  readOnly: false, last: true, action: null, limits: { maxOutputTokens: 4096, modelContext: 8192 },
  saved: null, onContinue: vi.fn(), onNewChat: vi.fn(), imageData: {}, imagesLoaded: true, onEditing: vi.fn(), onResend: vi.fn(),
});
const speech = (): NonNullable<ComponentProps<typeof MessageView>['speech']> => ({ state: { kind: 'idle' }, onListen: vi.fn(), onStop: vi.fn() });
const render = (p: ComponentProps<typeof MessageView>) => renderToStaticMarkup(createElement(MessageView, p));
describe('reply voice states', () => {
  it('shows Listen only with capability, a complete reply, and text', () => {
    const p = props(); expect(render(p)).not.toContain('listen');
    p.speech = speech(); expect(render(p)).toContain('>Listen</button>');
    p.live = true; expect(render(p)).not.toContain('class="ghost tiny listen"');
    p.live = false; p.message.status = 'stopped'; expect(render(p)).not.toContain('class="ghost tiny listen"');
    p.message.status = 'complete'; p.message.content = ''; expect(render(p)).not.toContain('class="ghost tiny listen"');
  });
  it('respects the shared blocked state without hiding capability', () => {
    const p = props(); p.speech = speech(); p.sendBlocked = true;
    expect(render(p)).toContain('class="ghost tiny listen" disabled=""');
  });
  it('keeps the reply meta and Copy while speech is making or playing', () => {
    const p = props(); p.speech = { ...speech(), state: { kind: 'making', id: 'reply', characters: 306 } };
    let html = render(p); expect(html).toContain('making speech on Test host…'); expect(html).toContain('>Stop</button>'); expect(html).toContain('>Copy</button>');
    p.speech.state = { kind: 'playing', id: 'reply', characters: 306, position: 9, duration: 22 };
    html = render(p); expect(html).toContain('0:09 / 0:22 · 306 characters · made on Test host'); expect(html).toContain('class="meta"');
    p.speech.state = { kind: 'playing', id: 'other', characters: 1, position: 0, duration: 2 };
    expect(render(p)).not.toContain('class="spoken"');
  });
  it.each([429, 502])('puts raw failure evidence under Details and leaves Listen available (%i)', (status) => {
    const p = props(); p.speech = { ...speech(), state: { kind: 'error', id: 'reply', error: new GatewayError(status, 'speech_budget_exhausted', '', 'budget spent', 12, undefined, undefined, '<script>raw refusal</script>') } };
    const html = render(p); expect(html).toContain(status === 429 ? 'Test host refused it.' : 'Test host couldn’t make it.'); expect(html).toContain('>Details</summary>'); expect(html).toContain('&lt;script&gt;raw refusal&lt;/script&gt;'); expect(html).not.toContain('<script>'); expect(html).toContain('>Listen</button>');
  });
});
