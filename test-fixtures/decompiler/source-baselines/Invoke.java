class Invoke {

  static int stat() {
    return 1;
  }

  int inst() {
    return 2;
  }

  int callStat() {
    return stat();
  }

  int callInst() {
    return this.inst();
  }

  int strLen(java.lang.String arg0) {
    return arg0.length();
  }
}
