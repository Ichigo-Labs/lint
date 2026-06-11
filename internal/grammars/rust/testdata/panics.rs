pub fn pick(n: u8) -> &'static str {
    match n {
        0 => panic!("zero is not allowed"),
        1 => "one",
        _ => unreachable!("bounded by caller"),
    }
}
