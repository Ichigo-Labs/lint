func boot(config: [String: Int]) {
    let port = config["port"]!
    print("booting on \(port)")
    let parsed = try! load()
    print("parsed", parsed)
}
