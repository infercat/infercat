// The harness owns agent semantics; this module only translates model wire data.
export function modelRequest(run, options) {
  const helper = options.purpose === "compaction" || options.purpose === "session-title";
  const messages = [];
  if (options.system) messages.push({role:'system',content:options.system});
  for (const message of options.messages) {
    if (!helper && message.id === run.promptID) {
      messages.push(...run.input.messages);
      continue;
    }
    let content = '', reasoning = '', calls = [];
    for (const block of message.content) {
      if (block.type === 'text') content += block.text;
      else if (block.type === 'reasoning') reasoning += block.text;
      else if (block.type === 'tool-call') calls.push({id:block.id,type:'function',function:{name:block.name,arguments:block.arguments}});
      else if (block.type === 'tool-result') messages.push({role:'tool',tool_call_id:block.toolCallId,content:block.content.map(b => {
        if (b.type !== 'text') throw Error('Unsupported native tool result content');
        return b.text;
      }).join('')});
      else throw Error('Unsupported native content: '+block.type);
    }
    if (content || reasoning || calls.length) messages.push({role:message.role,content,...(reasoning?{reasoning_content:reasoning}:{}),...(calls.length?{tool_calls:calls}:{})});
  }
  return {...(helper ? {temperature:options.temperature,chat_template_kwargs:{enable_thinking:false}} : run.input),model:options.model,messages,stream:true,
    ...(options.maxTokens?{max_tokens:options.maxTokens}:{}),
    ...(helper && options.stop ? {stop:options.stop}:{}),
    tools:options.tools?.map(t=>({type:'function',function:{name:t.name,description:t.description,parameters:t.parameters}})),
  };
}

export async function* modelChunks(source) {
  let pending='', nextIndex=0, finished=false, reason;
  const blocks=new Map();
  for await (const raw of source) {
    pending+=raw;
    if (pending.length>2*1024*1024) throw Error('Model frame limit');
    let end;
    while ((end=pending.indexOf('\n'))>=0) {
      const line=pending.slice(0,end).trim();pending=pending.slice(end+1);
      if (!line.startsWith('data:')) continue;
      const value=line.slice(5).trim();if(value==='[DONE]')continue;
      const event=JSON.parse(value);
      if(event.error)throw Error('Model request refused');
      const choice=event.choices?.[0],delta=choice?.delta;
      for(const [field,type] of [['reasoning_content','reasoning'],['content','text']]) {
        if(!delta?.[field])continue;
        let b=blocks.get(type);
        if(!b){b={index:nextIndex++,type,text:''};blocks.set(type,b);yield {type:'block-start',index:b.index,blockType:type};}
        b.text+=delta[field];yield {type:type==='text'?'text-delta':'reasoning-delta',index:b.index,text:delta[field]};
      }
      for(const call of delta?.tool_calls??[]) {
        const key='tool-'+call.index;let b=blocks.get(key);
        if(!b){b={index:nextIndex++,type:'tool-call',id:'',name:'',arguments:''};blocks.set(key,b);yield {type:'block-start',index:b.index,blockType:'tool-call'};}
        const newName=!b.name ? call.function?.name : undefined;
        const newID=!b.id?call.id:undefined;if(newID)b.id=newID;if(newName)b.name=newName;
        b.arguments+=call.function?.arguments??'';
        yield {type:'tool-call-delta',index:b.index,id:b.id,...(newName?{name:newName}:{}),argumentsDelta:call.function?.arguments??''};
      }
      if(event.usage)yield {type:'usage',usage:{inputTokens:event.usage.prompt_tokens??0,outputTokens:event.usage.completion_tokens??0,totalTokens:event.usage.total_tokens}};
      if(choice?.finish_reason && !finished) {
        finished=true;
        reason={kind:choice.finish_reason==='tool_calls'?'tool-calls':choice.finish_reason==='length'?'max-tokens':'stop'};
      }
    }
  }
  if(!finished)throw Error('Model stream ended without finish');
  for(const {index,...block} of blocks.values())yield {type:'block-end',index,block};
  yield {type:'finish',reason};
}
