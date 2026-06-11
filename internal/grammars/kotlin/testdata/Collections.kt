fun report(items: List<Int>, names: Map<String, Int>) {
    if (items.size == 0) {
        println("empty items")
    }
    if (names.size == 0) return
    println(items.sum())
}
