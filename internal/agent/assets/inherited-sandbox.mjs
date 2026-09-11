import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import {realpathSync} from 'node:fs';
const require=createRequire(process.env.INFERCAT_AGENT_RUNTIME+'/package.json');
const {SandboxProvider,SandboxUnavailableError}=await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-sandbox')).href);
export default class InheritedSandbox extends SandboxProvider {
 constructor(ctx,config){super(ctx);this.workspace=realpathSync(config.workspace);if(process.env.INFERCAT_CONFINED_WORKSPACE!==this.workspace)throw Error('outer confinement not established');}
 confine(argv,policy){
  if(policy.mode!=='workspace-write'||realpathSync(policy.workspaceRoot)!==this.workspace)throw new SandboxUnavailableError(policy.mode,'unsupported policy inside the outer run sandbox');
  return {argv:[...argv],enforcement:'full',denialSignatures:['operation not permitted','permission denied'],runnerFailureRules:[]};
 }
}
