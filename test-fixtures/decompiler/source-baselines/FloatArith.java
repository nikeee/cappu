class FloatArith {

  float mul(float arg0, float arg1) {
    return arg0 * arg1;
  }

  float addc(float arg0) {
    return arg0 + 0.1f;
  }

  double div(double arg0, double arg1) {
    return arg0 / arg1;
  }

  float neg(float arg0) {
    return -arg0;
  }

  double mix(double arg0, float arg1) {
    return arg0 + (double) arg1;
  }

  float rem(float arg0, float arg1) {
    return arg0 % arg1;
  }

  double poly(double arg0) {
    return arg0 * arg0 + arg0;
  }
}
