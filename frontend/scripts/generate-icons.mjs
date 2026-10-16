import {deflateSync} from 'node:zlib';
import {existsSync,mkdirSync,readFileSync,readdirSync,writeFileSync} from 'node:fs';
import {dirname} from 'node:path';

const polygon=[[.34,.25],[.66,.25],[.66,.75],[.5,.64],[.34,.75]];
const ink=[22,25,24], leaf=[187,213,140];
const crcTable=Array.from({length:256},(_,n)=>{for(let i=0;i<8;i++)n=n&1?0xedb88320^(n>>>1):n>>>1;return n>>>0});
function chunk(type,data){
 const tag=Buffer.from(type), length=Buffer.alloc(4), check=Buffer.alloc(4);
 length.writeUInt32BE(data.length);
 let crc=0xffffffff;
 for(const byte of Buffer.concat([tag,data]))crc=crcTable[(crc^byte)&255]^(crc>>>8);
 check.writeUInt32BE((crc^0xffffffff)>>>0);
 return Buffer.concat([length,tag,data,check]);
}
function inside(x,y){
 let hit=false;
 for(let i=0,j=polygon.length-1;i<polygon.length;j=i++){
  const a=polygon[i],b=polygon[j];
  if((a[1]>y)!==(b[1]>y)&&x<(b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0])hit=!hit;
 }
 return hit;
}
function encode(width,height,bytes){
 const header=Buffer.alloc(13);header.writeUInt32BE(width,0);header.writeUInt32BE(height,4);header[8]=8;header[9]=6;
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',header),chunk('IDAT',deflateSync(bytes)),chunk('IEND',Buffer.alloc(0))]);
}
function render(size,transparent){
 const bytes=Buffer.alloc((size*4+1)*size);
 for(let y=0;y<size;y++){
  const row=y*(size*4+1);bytes[row]=0;
  for(let x=0;x<size;x++){
   let count=0;
   for(let sy=0;sy<4;sy++)for(let sx=0;sx<4;sx++)if(inside((x+(sx+.5)/4)/size,(y+(sy+.5)/4)/size))count++;
   const coverage=count/16,at=row+1+x*4;
   for(let c=0;c<3;c++)bytes[at+c]=transparent?leaf[c]:Math.round(ink[c]*(1-coverage)+leaf[c]*coverage);
   bytes[at+3]=transparent?Math.round(coverage*255):255;
  }
 }
 return encode(size,size,bytes);
}
function save(path,size,transparent=false){mkdirSync(dirname(path),{recursive:true});writeFileSync(path,render(size,transparent))}
function splash(width,height){
 const bytes=Buffer.alloc((width*4+1)*height),unit=Math.min(width,height)*.85;
 for(let y=0;y<height;y++){
  const row=y*(width*4+1);bytes[row]=0;
  for(let x=0;x<width;x++){
   const u=.5+(x+.5-width/2)/unit,v=.5+(y+.5-height/2)/unit;
   const fill=u>.335&&u<.665&&v>.245&&v<.755&&inside(u,v);
   const at=row+1+x*4;
   for(let c=0;c<3;c++)bytes[at+c]=fill?leaf[c]:ink[c];
   bytes[at+3]=255;
  }
 }
 return encode(width,height,bytes);
}
save('ios/App/App/Assets.xcassets/AppIcon.appiconset/AppIcon-512@2x.png',1024);
for(const [density,size] of Object.entries({mdpi:48,hdpi:72,xhdpi:96,xxhdpi:144,xxxhdpi:192})){
 const base=`android/app/src/main/res/mipmap-${density}/`;
 save(base+'ic_launcher.png',size);
 save(base+'ic_launcher_round.png',size);
 save(base+'ic_launcher_foreground.png',size*9/4,true);
}
const iosSplash=splash(2732,2732);
for(const name of ['splash-2732x2732.png','splash-2732x2732-1.png','splash-2732x2732-2.png'])writeFileSync(`ios/App/App/Assets.xcassets/Splash.imageset/${name}`,iosSplash);
const resources='android/app/src/main/res';
for(const entry of readdirSync(resources,{withFileTypes:true})){
 if(!entry.isDirectory()||!entry.name.startsWith('drawable'))continue;
 const path=`${resources}/${entry.name}/splash.png`;
 if(!existsSync(path))continue;
 const original=readFileSync(path),width=original.readUInt32BE(16),height=original.readUInt32BE(20);
 writeFileSync(path,splash(width,height));
}
