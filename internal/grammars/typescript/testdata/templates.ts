const name = "wörld";
const greeting = `héllo ${name} 🎉 ${1 + 2} ${`nested ${name}`}`;
const sql = `SELECT *
FROM users
WHERE email = '${name}'`;
console.log(`${greeting} ${sql}`);
const same = name == name ? "yes" : name ? "truthy" : "no";
