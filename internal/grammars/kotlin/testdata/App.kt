fun main() {
    println("starting")
    val config: String? = load()
    val forced = config!!
    println("config $forced")
}
