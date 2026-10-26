/* Shared lossless range movement for editor and sortable admin lists. */
window.JellySortable = Object.freeze({
 moveRange(source,start,end,target){
  const lines=source.split('\n');
  if(start<1||end<start||end>lines.length||target<1||target>lines.length+1||target>=start&&target<=end+1)return source;
  const chunk=lines.splice(start-1,end-start+1);
  lines.splice(target-1-(target>end?chunk.length:0),0,...chunk);
  return lines.join('\n');
 }
});
