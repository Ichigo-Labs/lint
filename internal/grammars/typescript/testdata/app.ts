const conf: any = load();
var legacy = true;
function boot(a: string, b: number, c: boolean, d: string, e: number, f: string) {
  console.log("booting", a, b);
  if (a == b) {
    throw "mismatch";
  }
  setTimeout(() => refresh(), 600000);
  setTimeout(flush, 0);
}
