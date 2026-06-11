fn main() {
    let config = std::fs::read_to_string("cfg.toml").unwrap();
    println!("loaded {} bytes", config.len());
    let n: i32 = config.trim().parse().expect("not a number");
    dbg!(n);
}
