export function bridgeAudioFixture(fixture,state='on') {
 const emptyStats=Object.fromEntries(Object.entries(fixture.today.total).map(([key,value])=>[key,typeof value==='number'?0:typeof value==='string'?'':{}]));
 const d=structuredClone(fixture);
 d.status.bridge={enabled:state!=='off',url:'https://gateway.infercat.ai/h/maxws/v1',connected:state==='on',since:'2026-09-10T12:04:00Z',last_error:state==='error'?'closed 1006':'',requests_today:14};
 if(state==='absent')delete d.status.bridge;
 const split=(calls,tokens,requests=calls)=>({...emptyStats,model_calls:calls,requests,prompt_tokens:tokens});
 d.today.by_via={direct:split(298,175300,480),bridge:split(14,9120)};
 d.week.by_via={direct:split(1371,991000,1860),bridge:split(41,33330)};
 Object.assign(d.today.total,{seconds:252,characters:340});Object.assign(d.week.total,{seconds:1860,characters:2140});
 const id=d.keys[0].id;
 Object.assign(d.today.keys.find(k=>k.key_id===id),{seconds:12,characters:340});Object.assign(d.week.keys.find(k=>k.key_id===id),{seconds:96,characters:2140});
 Object.assign(d.keys[0].limits,{daily_audio_seconds:3600,daily_speech_chars:200000});
 return d;
}
