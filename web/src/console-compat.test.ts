import {expect,it} from 'vitest';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import current from './fixtures/console/current-085-dev.json';
import old from '../../console/test/fixture.json';
it('keeps new console settings optional relative to the read-only API',()=>{
 const source=ts.createSourceFile('types.ts',readFileSync(new URL('../../console/types.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true);
 const settings=source.statements.find((s):s is ts.InterfaceDeclaration=>ts.isInterfaceDeclaration(s)&&s.name.text==='Settings')!;
 for(const m of settings.members)if(!(m.name!.getText(source)in old.settings))expect((m as ts.PropertySignature).questionToken,`optional ${m.name!.getText(source)}`).toBeDefined();
});
it('captures current settings wire fields without labelling dev as a release',()=>{
 const wire=readFileSync(new URL('../../cmd/infercat/console.go',import.meta.url),'utf8').split('type consoleSettings struct {')[1]!.split('\n}')[0]!;
 for(const field of wire.matchAll(/json:"([^",]+)/g))expect(field[1]! in current.settings,`capture ${field[1]}`).toBe(true);
 expect(current.status.version).toContain('dev');expect(JSON.stringify(current)).not.toContain('Bearer ');
});
