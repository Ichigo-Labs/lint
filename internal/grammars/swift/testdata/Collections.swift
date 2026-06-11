func describe(items: [Int], label: String) -> Bool {
    if items.count == 0 {
        print("empty \(label)")
        return true
    }
    return label.count == 0
}
