import OpenAI from 'openai';
import { connect } from '../dist/index.js';

export async function run(invite: string, wasmURL: string) {
  const session = await connect(invite, { wasmURL });
  try {
    const client = new OpenAI(session.openai());
    const models = (await client.models.list()).data.map((model) => model.id);
    if (!models.length) throw new Error('The real host listed no models');
    const stream = await client.chat.completions.create({
      model: models[0]!, messages: [{ role: 'user', content: 'Reply with a short hello.' }],
      max_tokens: 32, stream: true,
    });
    const started = performance.now();
    const arrivals: number[] = [];
    let text = '';
    for await (const event of stream) {
      const content = event.choices[0]?.delta.content;
      if (content) { text += content; arrivals.push(Math.round(performance.now() - started)); }
    }
    return { models, text, arrivals, status: session.status };
  } finally { session.close(); }
}
