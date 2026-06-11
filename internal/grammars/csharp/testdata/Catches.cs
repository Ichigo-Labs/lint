class Catches {
    void Swallow() {
        try { F(); } catch { }
    }

    void SwallowTyped() {
        try { F(); } catch (Exception ex) { }
    }

    void Logged() {
        try { F(); } catch (Exception ex) { Log(ex); }
    }
}
