import { runs, scriptedRun, updateRun } from './fake-runs';
import { handleImageJobs } from './fake-image-jobs';
import type { FakeRequest, FakeResponse } from './fake-backend';
export function fakeChatTools(req: FakeRequest): FakeResponse | undefined {
 const body=JSON.parse(req.body||'{}'); if(!body.host_tools?.length) return;
 const id=`chat-${runs.size+1}`, record={...scriptedRun(id,'running'),kind:'chat',client_request_id:body.client_request_id,input:body,steps:[],outputs:[],key_id:'k_7f3a2b'};
 runs.set(id,record);updateRun(id,{});
 return {status:200,headers:{'content-type':'text/event-stream'},sse:(async function*(){
  yield `event: run\ndata: ${JSON.stringify({run_id:id})}\n\n`;
  yield `data: ${JSON.stringify({choices:[{delta:{content:body.host_tools.includes('make_image')?'Your fox pictures are being made.':'I found four sources about foxes.'}}]})}\n\n`;
  const steps=[];
  if(body.host_tools.includes('web_search')) steps.push({id:'search-1',type:'step' as const,at:record.created,kind:'search' as const,tool:'web_search',name:'Search web',result:'4 results',output_id:'search',status:'done' as const});
  if(body.host_tools.includes('make_image')) {
  const step={id:'tool-1',type:'step' as const,at:record.created,kind:'other' as const,tool:'make_image',name:'Make image',result:'submitting image jobs; outcome not confirmed',status:'running' as const};updateRun(id,{steps:[step]});
  const response=handleImageJobs({...req,path:'/v1/images/jobs',body:JSON.stringify({prompts:['A red fox in snow.','A fox under a tree.'],conversation:body.conversation,client_request_id:body.client_request_id})})!;
  for(const job of JSON.parse(response.body!).jobs) updateRun(job.id,{input:{...job.input,parent_run_id:id,tool_call_id:step.id}});
  steps.push({...step,status:'done' as const,result:'submitted 2 image jobs'});
  }
  await new Promise(resolve=>setTimeout(resolve,250));
  updateRun(id,{state:'done',steps,attempts:[{id:'model',dispatched:true,settled:true,accounting_uncertain:false,usage:{prompt_tokens:100,completion_tokens:20}}]});
  yield 'data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20}}\n\ndata: [DONE]\n\n';
 })()};
}
