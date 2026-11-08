import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import {Runtime} from '../../packages/runtime/src/runtime.ts';
import {SimulatedCrash} from '../../packages/runtime/src/errors.ts';
import {Store} from '../../packages/runtime/src/store.ts';
import {vitestPlugin} from '../../plugins/vitest/src/index.ts';
const root=fs.mkdtempSync(path.join(os.tmpdir(),'prepare-audit-'));
try {
 const rt=Runtime.open(root);
 const req=rt.store.createRecord('requirement',{title:'audit'},'original');
 const q=rt.questions.create({kind:'free_text',prompt:'audit',subjects:[req.id]});
 const {answer}=rt.questions.answer(q.id,{text:'confirmed'});
 const observed=[];
 rt.store.emitter.on('question',payload=>{
  const a=payload.answers.find(a=>a.answer_id===answer.answer_id);
  if(a?.applied_as) observed.push({state:payload.state,applied_as:!!a.applied_as,committed:rt.store.isCommitted(a.applied_as.txid)});
 });
 try {rt.questions.apply(q.id,answer.answer_id,{decision:{title:'audit',body:'audit'}},{afterStage(stage){if(stage==='question')throw new SimulatedCrash(stage)}});} catch(e){if(!(e instanceof SimulatedCrash))throw e;}
 assert(observed.some(x=>x.applied_as&&!x.committed));
 console.log('precommit_question_event',JSON.stringify(observed));
 fs.appendFileSync(path.join(root,req.path),'\nchanged while stopped\n');
 const original=Store.prototype.appendEvent;
 const fault=new Error('audit crash immediately after abort journal append');
 Store.prototype.appendEvent=function(ev){const result=original.call(this,ev);if(ev.type==='apply.abort')throw fault;return result;};
 try {Runtime.open(root);throw new Error('abort injection did not run');} catch(e){if(e!==fault)throw e;} finally {Store.prototype.appendEvent=original;}
 const st=Store.open(root);
 const persisted=st.readQuestion(q.id);
 const intermediate={inflight:st.inflightIntents().length,applied_as:!!persisted.answers[0].applied_as,aborted:st.abortedTxids().size};
 assert.equal(intermediate.inflight,0);assert.equal(intermediate.applied_as,true);assert.equal(intermediate.aborted,1);
 console.log('crash_during_abort_cleanup',JSON.stringify(intermediate));
 const recovered=Runtime.open(root);
 assert.equal(recovered.questions.read(q.id).answers[0].applied_as,null);
 console.log('next_restart_cleanup','OK');
 const project=path.join(root,'inputs');fs.mkdirSync(project);
 for(const [file,body] of Object.entries({'package.json':'{"name":"audit","type":"module"}','pnpm-lock.yaml':'lockfileVersion: 9.0\n','entry.test.ts':'export const testInput=1;','extra.ts':'import { value } from "./deep.ts"; export {value};','deep.ts':'export const value=1;'}))fs.writeFileSync(path.join(project,file),body);
 const job={root:project,run_id:'RUN-audit',plugin:'vitest',claims:[],targets:['entry.test.ts'],config:{impl_files:['extra.ts']}};
 const before=await vitestPlugin.inputs(job);
 fs.appendFileSync(path.join(project,'pnpm-lock.yaml'),'changed: true\n');
 fs.writeFileSync(path.join(project,'deep.ts'),'export const value=2;');
 const after=await vitestPlugin.inputs(job);
 assert.deepEqual(after,before);
 console.log('input_inventory_after_dependency_changes',JSON.stringify({unchanged:true,tracked:[...after.targets,...after.files].map(f=>f.path),untracked:['package.json','pnpm-lock.yaml','deep.ts']}));
}finally{fs.rmSync(root,{recursive:true,force:true});}
