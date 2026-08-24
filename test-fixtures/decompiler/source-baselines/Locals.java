class Locals {

  int compute(int arg0) {
    int var2 = arg0 + 1;
    int var3 = var2 * 2;
    int var4 = var2 + var3;
    return var4;
  }

  long widen(int arg0) {
    long var2 = (long) arg0;
    return var2 + 1L;
  }

  int reassign(int arg0) {
    int var2 = arg0;
    var2 = var2 + var2;
    return var2;
  }
}
