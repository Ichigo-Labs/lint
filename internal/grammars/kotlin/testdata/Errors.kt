fun fragile() {
    try {
        work()
    } catch (e: Exception) { }
}

fun logged() {
    try {
        work()
    } catch (e: Exception) {
        println("failed: $e")
    }
}
