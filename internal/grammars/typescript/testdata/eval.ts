export function dangerous(input: string) {
  const out = eval(input);
  alert("evaluated");
  return out;
}
