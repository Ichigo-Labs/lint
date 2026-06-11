console.log("flagged");
console.log("silenced one rule"); // lint:ignore ts-no-console
console.log("silenced all rules"); // lint:ignore
// lint:ignore-next-line ts-no-console
console.log("silenced by previous line");
eval("1+1"); // lint:ignore ts-no-eval,auto-no-eval safe in this sandbox
