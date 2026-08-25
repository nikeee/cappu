class Arithmetic {

  int add(int arg0, int arg1) {
    return arg0 + arg1;
  }

  int poly(int arg0, int arg1, int arg2) {
    return arg0 * arg1 + arg2;
  }

  long mix(int arg0, long arg1) {
    return (long) arg0 + arg1;
  }

  double dm(double arg0, int arg1) {
    return arg0 * (double) arg1;
  }

  int shift(int arg0, int arg1) {
    return arg0 << arg1;
  }

  int bits(int arg0, int arg1) {
    return arg0 & arg1 | arg0 ^ arg1;
  }

  int neg(int arg0) {
    return -arg0;
  }

  int not(int arg0) {
    return arg0 ^ -1;
  }

  int rem(int arg0, int arg1) {
    return arg0 % arg1;
  }
}
